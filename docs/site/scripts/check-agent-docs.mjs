// Checks the files for agents in the built site (dist/): llms.txt and the setup guide.
//
//   1. Both files are in dist/, at the root of the site.
//   2. Every command that the guide names exists in the help of the programs: "office <command>"
//      in "office help", the flags of the command in "office help <command>", and "office-hub"
//      with its flags in "office-hub -h". The guide must name at least one command.
//   3. Every link of the two files to this site leads to a page (and to an anchor) of dist/.
//
// The build fails on the first problem. Run it after the site and the architecture are built.
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { agentFiles, base, origin } from "../site-config.mjs";

const siteDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const root = path.resolve(siteDir, "../..");
const dist = path.join(siteDir, "dist");
const binDir = path.resolve(process.env.OFFICE_BIN_DIR ?? path.join(root, "bin"));
const guideName = "agent-setup.md";

const problems = [];
const problem = (message) => problems.push(message);

// --- 1. The files are served -----------------------------------------------------------------

const texts = new Map();
for (const name of agentFiles) {
  const file = path.join(dist, name);
  if (!existsSync(file)) problem(`dist/${name} is missing: the site does not serve it`);
  else texts.set(name, readFileSync(file, "utf8"));
  if (texts.get(name)?.includes("{{")) problem(`dist/${name} has an unresolved {{placeholder}}`);
}
const llms = texts.get("llms.txt");
const guide = texts.get(guideName);
if (llms !== undefined) {
  const lines = llms.split("\n");
  if (!/^# \S/.test(lines[0])) problem("llms.txt must start with a title: '# Name'");
  if (!lines.some((l) => l.startsWith("> "))) problem("llms.txt needs a summary in a block quote: '> ...'");
  if (!llms.includes(`${origin}${base}/${guideName}`)) problem(`llms.txt must link to the setup guide ${origin}${base}/${guideName}`);
  const setup = llms.indexOf(`${origin}${base}/${guideName}`);
  const others = [...llms.matchAll(/\]\((https?:[^)]+)\)/g)].filter((m) => !m[1].endsWith(guideName));
  if (others.some((m) => m.index < setup)) problem("llms.txt must name the setup guide before every other link");
}

// --- 2. The commands of the guide ------------------------------------------------------------

function help(binary, args, allowFailure = false) {
  const file = path.join(binDir, binary);
  if (!existsSync(file)) {
    console.error(`docs: ${file} is missing: build the programs first (make build)`);
    process.exit(1);
  }
  try {
    return execFileSync(file, args, { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
  } catch (err) {
    if (allowFailure && err.stdout) return String(err.stdout);
    throw err;
  }
}

/** The names of the commands in "office help": the indented lines of the groups, and "help". */
function commandNames(overview) {
  const names = new Set(["help"]);
  for (const line of overview.split("\n")) {
    const m = line.match(/^ {2}([a-z][a-z -]*?) {2,}\S/);
    if (m) names.add(m[1]);
  }
  return names;
}

/** The code of a Markdown file: every line of a code fence and every inline code span. */
function codeOf(text) {
  const out = [];
  let fence = false;
  for (const line of text.split("\n")) {
    if (/^\s*(`{3,}|~{3,})/.test(line)) {
      fence = !fence;
      continue;
    }
    if (fence) out.push(line);
    else for (const m of line.matchAll(/`([^`]+)`/g)) out.push(m[1]);
  }
  return out;
}

const flagsOf = (text) => [...text.matchAll(/(?<![\w-])--[a-z][a-z0-9-]*/g)].map((m) => m[0]);

if (guide !== undefined) {
  const names = commandNames(help("office", ["help"]));
  if (names.size < 10) problem("could not read the commands from 'office help'");
  const helpCache = new Map();
  const helpOf = (command) => {
    if (!helpCache.has(command)) helpCache.set(command, help("office", ["help", ...command.split(" ")]));
    return helpCache.get(command);
  };
  const hubHelp = help("office-hub", ["-h"], true);
  let checked = 0;

  for (const raw of codeOf(guide)) {
    const line = raw.replace(/\s+#.*$/, "").replace(/^\s*\$\s+/, "");
    for (const m of line.matchAll(/(?<![\w./-])office(-hub)?(?:\)")?(?=\s)\s+(.*)$/g)) {
      const rest = m[2].split(/\s+--\s+/)[0]; // the harness command after "--" is not ours
      const tokens = rest.split(/\s+/).filter(Boolean);
      if (m[1]) {
        // office-hub: a subcommand or flags.
        checked++;
        if (tokens[0] && !tokens[0].startsWith("-") && !tokens[0].startsWith("<") && !["keygen", "help"].includes(tokens[0])) {
          problem(`"${line}": office-hub has no command "${tokens[0]}"`);
        }
        for (const flag of tokens.filter((t) => /^-[a-z]/.test(t))) {
          if (!["-h", "-help"].includes(flag) && !new RegExp(`(^|\\s)${flag}(\\s|$)`, "m").test(hubHelp)) {
            problem(`"${line}": office-hub has no flag ${flag} in 'office-hub -h'`);
          }
        }
        continue;
      }
      if (tokens.length === 0 || /^[-<"$[(]/.test(tokens[0])) continue; // "office" as a word, or a flag first
      const words = [];
      for (const t of tokens) {
        if (/^[-<"'$[(>|&;#]/.test(t) || t.includes("=") || words.length === 3) break;
        words.push(t);
      }
      let command = null;
      for (let n = words.length; n >= 1 && !command; n--) {
        const candidate = words.slice(0, n).join(" ");
        if (names.has(candidate)) command = candidate;
      }
      if (!command) {
        problem(`"${line}": 'office ${words.join(" ")}' is not a command in 'office help'`);
        continue;
      }
      checked++;
      const known = new Set(flagsOf(helpOf(command)));
      for (const flag of flagsOf(rest)) {
        if (!known.has(flag)) problem(`"${line}": 'office help ${command}' has no flag ${flag}`);
      }
    }
  }
  if (checked < 10) problem(`the guide names only ${checked} commands: the check cannot find them`);
  else console.log(`docs: ${guideName}: ${checked} commands and their flags exist`);
}

// --- 3. The links to this site ---------------------------------------------------------------

function pageFile(urlPath) {
  const rel = decodeURIComponent(urlPath.slice(base.length)).replace(/^\/+/, "");
  const candidates = rel === "" || rel.endsWith("/") ? [path.join(rel, "index.html")] : [rel, path.join(rel, "index.html")];
  return candidates.map((c) => path.join(dist, c)).find((f) => existsSync(f));
}

for (const [name, text] of texts) {
  const urls = new Set([...text.matchAll(/https?:\/\/[^\s)>\]"'`]+/g)].map((m) => m[0].replace(/[.,;]+$/, "")));
  for (const url of urls) {
    if (!url.startsWith(`${origin}${base}/`) && url !== `${origin}${base}`) continue;
    const [urlPath, anchor] = url.slice(origin.length).split("#");
    const file = pageFile(urlPath);
    if (!file) {
      problem(`${name}: ${url} leads to no page in dist/`);
    } else if (anchor && file.endsWith(".html") && !readFileSync(file, "utf8").includes(`id="${anchor}"`)) {
      problem(`${name}: ${url}: the page has no anchor #${anchor}`);
    }
  }
}

if (problems.length > 0) {
  console.error("docs: the files for agents have problems:");
  for (const p of problems) console.error(`  - ${p}`);
  process.exit(1);
}
console.log(`docs: llms.txt and ${guideName} are in dist/, and their links work`);
