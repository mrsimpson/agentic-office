// Adds the architecture to the built site as its section: the output of "arc42 build".
import { execFileSync } from "node:child_process";
import { existsSync, rmSync } from "node:fs";
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
console.log(`docs: the architecture is at ${base}/${architecturePath}/`);
