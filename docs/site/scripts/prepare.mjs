// Fills src/content/docs from the files of the repository. The pages are build output: nothing
// they hold is committed. A fact lives in one file; the site reads it at build time.
//
//   USER_GUIDE.md   -> user-guide/   one page per chapter, with the anchors of the guide
//   README.md, tests/README.md, AGENTS.md -> developers/
//   office help, office-hub -h -> reference/   the real help of the built programs
//
// Links between these files work on GitHub and on the site: this script rewrites them for the site.
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import GithubSlugger from "github-slugger";
import { agentFiles, architecturePath, base, branch, origin, repository } from "../site-config.mjs";

const siteDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const root = path.resolve(siteDir, "../..");
const contentDir = path.join(siteDir, "src/content/docs");
const binDir = path.resolve(process.env.OFFICE_BIN_DIR ?? path.join(root, "bin"));

function fail(message) {
  console.error(`docs: ${message}`);
  process.exit(1);
}

// ---------------------------------------------------------------------------------------------
// Markdown helpers
// ---------------------------------------------------------------------------------------------

/** Calls visit(line, inFence) for every line; code fences and their lines are marked. */
function eachLine(text, visit) {
  let fence = null;
  return text.split("\n").map((line) => {
    const open = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence === null) {
      if (open) {
        fence = open[1];
        return visit(line, true);
      }
      return visit(line, false);
    }
    if (open && open[1][0] === fence[0] && open[1].length >= fence.length && line.trim() === open[1]) {
      fence = null;
    }
    return visit(line, true);
  });
}

function headingOf(line) {
  const m = line.match(/^(#{1,6})\s+(.*?)\s*#*\s*$/);
  return m ? { level: m[1].length, text: m[2] } : null;
}

/** The text of a heading as the renderer sees it: without code marks and link syntax. */
function plainHeading(text) {
  return text.replace(/\[([^\]]*)\]\([^)]*\)/g, "$1").replace(/[`*_]/g, "");
}

function slugify(text) {
  return text
    .toLowerCase()
    .replace(/^\d+\.\s*/, "")
    .replace(/[^a-z0-9\s-]/g, "")
    .trim()
    .replace(/\s+/g, "-");
}

function frontmatter(fields) {
  const lines = ["---"];
  for (const [key, value] of Object.entries(fields)) {
    if (value === undefined) continue;
    if (key === "order") lines.push(`sidebar:\n  order: ${value}`);
    else lines.push(`${key}: ${JSON.stringify(value)}`);
  }
  lines.push("---", "");
  return lines.join("\n");
}

function write(rel, text) {
  const file = path.join(contentDir, rel);
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, text);
}

// ---------------------------------------------------------------------------------------------
// Pages from Markdown files
// ---------------------------------------------------------------------------------------------

/** A page: its URL path below the content folder, its title and its lines (without frontmatter). */
const sources = new Map(); // repository path -> { pages, anchors: Map(anchor -> { page, chapter }) }

function pageUrl(dir) {
  return `${base}/${dir}${dir ? "/" : ""}`;
}

/** The file of a page: a section index is a folder with an index, any other page is one file. */
const sectionIndexes = new Set(["user-guide", "developers", "reference"]);
function fileOf(dir) {
  return sectionIndexes.has(dir) ? `${dir}/index.md` : `${dir}.md`;
}

function readSource(repoPath) {
  return readFileSync(path.join(root, repoPath), "utf8");
}

/** Splits USER_GUIDE.md: the part before the first chapter and one page per "## " chapter. */
function splitUserGuide(text) {
  const pages = [];
  let current = { dir: "user-guide", title: "User guide", lines: [], chapter: null };
  pages.push(current);
  let first = true;
  eachLine(text, (line, inFence) => {
    const h = inFence ? null : headingOf(line);
    if (h && h.level === 1 && first) {
      first = false;
      current.h1 = line;
      return;
    }
    if (h && h.level === 2) {
      const title = plainHeading(h.text);
      current = { dir: `user-guide/${slugify(title)}`, title, lines: [], chapter: line };
      pages.push(current);
      return;
    }
    current.lines.push(line);
  });
  return pages;
}

function singlePage(text, dir, title) {
  const lines = [];
  let first = true;
  eachLine(text, (line, inFence) => {
    const h = inFence ? null : headingOf(line);
    if (h && h.level === 1 && first) {
      first = false;
      return;
    }
    lines.push(line);
  });
  return [{ dir, title, lines, chapter: null }];
}

/** Anchors of a source file as GitHub computes them, with the page that shows each heading. */
function collectAnchors(text, pages) {
  const anchors = new Map();
  const slugger = new GithubSlugger();
  const all = [];
  eachLine(text, (line, inFence) => {
    const h = inFence ? null : headingOf(line);
    if (h) all.push({ line, text: plainHeading(h.text) });
  });
  const byLine = new Map();
  for (const page of pages) {
    if (page.h1) byLine.set(page.h1, { page, chapter: true });
    if (page.chapter) byLine.set(page.chapter, { page, chapter: true });
    for (const l of page.lines) if (headingOf(l)) byLine.set(l, { page, chapter: false });
  }
  for (const h of all) {
    const anchor = slugger.slug(h.text);
    const where = byLine.get(h.line);
    if (where) anchors.set(anchor, where);
  }
  return anchors;
}

function register(repoPath, text, pages) {
  sources.set(repoPath, { pages, anchors: collectAnchors(text, pages) });
}

const userGuide = readSource("USER_GUIDE.md");
register("USER_GUIDE.md", userGuide, splitUserGuide(userGuide));
const readme = readSource("README.md");
register("README.md", readme, singlePage(readme, "developers", "Build and test"));
const testsReadme = readSource("tests/README.md");
register("tests/README.md", testsReadme, singlePage(testsReadme, "developers/testing", "How the office is tested"));
const agents = readSource("AGENTS.md");
register("AGENTS.md", agents, singlePage(agents, "developers/contributing", "Rules for contributors and agents"));

/** Where a repository path lives on the site, or null if the site does not show it. */
function siteTarget(repoPath, anchor) {
  const clean = repoPath.replace(/\/+$/, "");
  if (clean === "docs/architecture" || clean.startsWith("docs/architecture/")) {
    return `${base}/${architecturePath}/`;
  }
  const source = sources.get(clean);
  if (!source) return null;
  if (!anchor) return pageUrl(source.pages[0].dir);
  const where = source.anchors.get(anchor);
  if (!where) fail(`${repoPath}#${anchor}: no such heading`);
  return pageUrl(where.page.dir) + (where.chapter ? "" : `#${anchor}`);
}

function githubUrl(repoPath, anchor) {
  const full = path.join(root, repoPath);
  const kind = existsSync(full) && statSync(full).isDirectory() ? "tree" : "blob";
  return `${repository}/${kind}/${branch}/${repoPath.replace(/\/+$/, "")}${anchor ? `#${anchor}` : ""}`;
}

function rewriteLinks(page, fromFile) {
  const dir = path.posix.dirname(fromFile);
  return eachLine(page.lines.join("\n"), (line, inFence) => {
    if (inFence) return line;
    return line.replace(/\]\(([^)\s]+)\)/g, (match, target) => {
      if (/^[a-z][a-z0-9+.-]*:/i.test(target)) return match;
      const [file, anchor] = target.split("#");
      const repoPath = file === "" ? fromFile : path.posix.normalize(path.posix.join(dir, file));
      if (repoPath.startsWith("..")) fail(`${fromFile}: link ${target} leaves the repository`);
      if (!existsSync(path.join(root, repoPath))) fail(`${fromFile}: link ${target} points to a missing file`);
      const onSite = siteTarget(repoPath, anchor);
      if (onSite) {
        // A link inside the same page stays an anchor link.
        if (file === "" && sources.get(fromFile).anchors.get(anchor)?.page === page) return `](#${anchor})`;
        return `](${onSite})`;
      }
      return `](${githubUrl(repoPath, anchor)})`;
    });
  }).join("\n");
}

/** Chapter pages: "###" becomes "##", so the table of contents of a page starts at its top level. */
function demote(text) {
  return eachLine(text, (line, inFence) => {
    if (inFence) return line;
    const h = headingOf(line);
    return h && h.level > 2 ? line.slice(1) : line;
  }).join("\n");
}

/** The ids that the site gives to the headings of a page must be those of GitHub. */
function checkAnchors(repoPath, page, text) {
  const slugger = new GithubSlugger();
  const want = [...sources.get(repoPath).anchors].filter(([, w]) => w.page === page && !w.chapter).map(([a]) => a);
  const got = [];
  eachLine(text, (line, inFence) => {
    const h = inFence ? null : headingOf(line);
    if (h) got.push(slugger.slug(plainHeading(h.text)));
    return line;
  });
  for (const anchor of want) {
    if (!got.includes(anchor)) fail(`${repoPath}: the anchor #${anchor} would change on the site (a heading occurs twice?)`);
  }
}

function trimBlank(text) {
  return text.replace(/^\s*\n/, "").replace(/\s+$/, "") + "\n";
}

rmSync(contentDir, { recursive: true, force: true });

const editUrl = (repoPath) => `${repository}/edit/${branch}/${repoPath}`;
for (const [repoPath, source] of sources) {
  const isGuide = repoPath === "USER_GUIDE.md";
  source.pages.forEach((page, index) => {
    let body = rewriteLinks(page, repoPath);
    if (isGuide) body = demote(body);
    checkAnchors(repoPath, page, body);
    const developers = page.dir.startsWith("developers");
    const order = isGuide ? index : developers ? { developers: 0, "developers/testing": 1, "developers/contributing": 2 }[page.dir] : undefined;
    write(
      fileOf(page.dir),
      frontmatter({ title: page.title, editUrl: editUrl(repoPath), order }) + trimBlank(body),
    );
  });
}

// ---------------------------------------------------------------------------------------------
// Start page
// ---------------------------------------------------------------------------------------------

{
  // The first paragraph of the README says what the office is.
  const intro = readme.split("\n").slice(2).join("\n").split(/\n\s*\n/)[0].replace(/\n/g, " ");
  if (!/office/i.test(intro)) fail("the first paragraph of README.md changed: check the start page");
  const template = readFileSync(path.join(siteDir, "start-page.mdx"), "utf8");
  write("index.mdx", template.replaceAll("{{base}}", base).replace("{{intro}}", intro));
}

// ---------------------------------------------------------------------------------------------
// Files for agents: llms.txt and the setup guide, served as they are at the root of the site
// ---------------------------------------------------------------------------------------------

{
  const publicDir = path.join(siteDir, "public");
  rmSync(publicDir, { recursive: true, force: true });
  mkdirSync(publicDir, { recursive: true });
  for (const name of agentFiles) {
    const text = readFileSync(path.join(siteDir, "agent", name), "utf8");
    writeFileSync(path.join(publicDir, name), text.replaceAll("{{site}}", `${origin}${base}`));
  }
}

// ---------------------------------------------------------------------------------------------
// Command reference from the real help
// ---------------------------------------------------------------------------------------------

function run(binary, args, { allowFailure = false } = {}) {
  const file = path.join(binDir, binary);
  if (!existsSync(file)) fail(`${file} is missing: build the programs first (make build)`);
  try {
    return execFileSync(file, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).replace(/\s+$/, "");
  } catch (err) {
    if (allowFailure && err.stdout) return String(err.stdout).replace(/\s+$/, "");
    fail(`${binary} ${args.join(" ")} failed: ${err.message}`);
  }
}

/** A fenced block that its own content cannot close. */
function codeBlock(text) {
  const longest = Math.max(2, ...(text.match(/`+/g) ?? []).map((m) => m.length));
  const fence = "`".repeat(longest + 1);
  return `${fence}text\n${text}\n${fence}\n`;
}

function parseOverview(help) {
  const groups = [];
  let group = null;
  let topics = [];
  for (const line of help.split("\n")) {
    const topic = line.match(/a topic: (.+)$/);
    if (topic) topics = topic[1].split(",").map((t) => t.trim());
    const heading = line.match(/^([A-Z][^:]*):$/);
    if (heading) {
      group = { title: heading[1], commands: [] };
      groups.push(group);
      continue;
    }
    const entry = line.match(/^ {2}(\S.*?) {2,}(\S.*)$/);
    if (entry && group && !entry[1].startsWith("office ")) {
      group.commands.push({ name: entry[1], summary: entry[2] });
    }
  }
  return { groups: groups.filter((g) => g.commands.length > 0), topics };
}

{
  const overview = run("office", ["help"]);
  const { groups, topics } = parseOverview(overview);
  const commandCount = groups.reduce((n, g) => n + g.commands.length, 0);
  if (commandCount < 10 || topics.length === 0) fail("could not read the list of commands from 'office help'");

  const links = [];
  groups.forEach((group, index) => {
    const slug = slugify(group.title.replace(/\(.*\)/, ""));
    const lines = [
      `The commands of the group "${group.title}". The text of each command is the output of \`office help <command>\`.`,
      "",
    ];
    for (const command of group.commands) {
      const help = run("office", ["help", ...command.name.split(" ")]);
      lines.push(`## ${command.name}`, "", `${command.summary}.`.replace(/\.\.$/, "."), "", codeBlock(help));
    }
    write(
      `reference/${slug}.md`,
      frontmatter({ title: group.title, order: index + 1 }) + trimBlank(lines.join("\n")),
    );
    links.push(`- [${group.title}](${pageUrl(`reference/${slug}`)}): ${group.commands.map((c) => `\`${c.name}\``).join(", ")}`);
  });

  const topicLines = ["Topics explain a subject across commands. The text of each topic is the output of `office help <topic>`.", ""];
  for (const topic of topics) {
    topicLines.push(`## ${topic}`, "", codeBlock(run("office", ["help", topic])));
  }
  write(`reference/topics.md`, frontmatter({ title: "Topics", order: groups.length + 1 }) + trimBlank(topicLines.join("\n")));

  const hub = run("office-hub", ["-h"], { allowFailure: true });
  if (!hub.includes("usage:")) fail("could not read the help of office-hub");
  write(
    `reference/office-hub.md`,
    frontmatter({ title: "office-hub", order: groups.length + 2 }) +
      trimBlank(["The hub has flags and two small commands. The text is the output of `office-hub -h`.", "", codeBlock(hub)].join("\n")),
  );

  write(
    "reference/index.md",
    frontmatter({ title: "Command reference", order: 0 }) +
      trimBlank(
        [
          "This reference comes from the programs themselves. The build of this site runs `office help`,",
          "`office help <command>`, `office help <topic>` and `office-hub -h` and prints what they say,",
          "so the reference cannot differ from the programs.",
          "",
          "The command line explains itself in the same way:",
          "",
          "- `office help` lists the commands.",
          "- `office <command> --help` shows the purpose, the flags with their defaults and an example.",
          "- `office help <topic>` explains a topic.",
          "- `office-hub -h` explains the hub.",
          "",
          "## Commands of `office`",
          "",
          ...links,
          "",
          `- [Topics](${pageUrl("reference/topics")}): ${topics.map((t) => `\`${t}\``).join(", ")}`,
          "",
          "## The hub",
          "",
          `- [office-hub](${pageUrl("reference/office-hub")}): the flags of the hub and how to start it`,
          "",
          "## Overview as the program prints it",
          "",
          codeBlock(overview),
        ].join("\n"),
      ),
  );
  console.log(`docs: ${commandCount} commands in ${groups.length} groups, ${topics.length} topics`);
}
