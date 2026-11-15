// Adds the architecture to the built site as its section: the output of "arc42 build".
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { arc42, architecturePath, base } from "../site-config.mjs";

const siteDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const out = path.join(siteDir, "dist", architecturePath);

if (!existsSync(path.join(siteDir, "dist", "index.html"))) {
  console.error("docs: dist/index.html is missing: build the site first");
  process.exit(1);
}
rmSync(out, { recursive: true, force: true });
execFileSync(
  "npx",
  [...arc42, "--dir", path.resolve(siteDir, "../architecture"), "build", "--out", out, "--base", `${base}/${architecturePath}/`],
  { stdio: "inherit" },
);
if (!existsSync(path.join(out, "index.html"))) {
  console.error("docs: the architecture site has no index.html");
  process.exit(1);
}

// A small fixed link back to the docs. The architecture site is a separate application, so the build
// adds the link to its page. If the place for it is gone, the build fails: look at the output of
// "arc42 build" and change the place here.
const page = path.join(out, "index.html");
const anchor = '<div id="root"></div>';
const html = readFileSync(page, "utf8");
if (html.split(anchor).length !== 2) {
  console.error(`docs: cannot add the link back to the docs: ${anchor} is not in the architecture page exactly once`);
  process.exit(1);
}
const backLink = `${anchor}
    <a class="back-to-docs" href="${base}/">&larr; Back to the docs</a>
    <style>
      .back-to-docs { position: fixed; right: 12px; top: 12px; z-index: 1000; padding: 4px 10px;
        font: 13px/1.4 system-ui, sans-serif; text-decoration: none; border-radius: 6px;
        color: #1f2933; background: #ffffffd9; border: 1px solid #c4cdd5; }
      .back-to-docs:hover { text-decoration: underline; }
      @media (prefers-color-scheme: dark) {
        .back-to-docs { color: #e4e7eb; background: #1f2933d9; border-color: #52606d; }
      }
    </style>`;
writeFileSync(page, html.replace(anchor, backLink));

console.log(`docs: the architecture is at ${base}/${architecturePath}/`);
