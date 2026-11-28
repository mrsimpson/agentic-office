#!/usr/bin/env node
// Builds the base style sheet of the stage and the portal (make ui-css).
//
// Input: the pinned packages @db-ux/core-foundations and @db-ux/core-components (DB UX Design
// System version 3, plain style sheets) in node_modules, and theme.css (our own theme).
// Output, the same bytes twice (each building block serves its own copy, dec-ui-base):
//   hub/stage/web/embed/base.css   hub/stage/web/embed/third-party-notices.txt
//   hub/portal/web/embed/base.css  hub/portal/web/embed/third-party-notices.txt
//
// The hub build needs none of this: the output is committed. Plain Node, no dependencies of its own.
"use strict";
const fs = require("node:fs");
const path = require("node:path");

const root = path.resolve(__dirname, "..", "..");
const modules = path.join(__dirname, "node_modules", "@db-ux");
const foundations = path.join(modules, "core-foundations", "build", "styles");
const components = path.join(modules, "core-components", "build");

// Parts of the foundations: the tokens and the defaults of the elements. Left out on purpose:
//   default-icons.css       holds an icon font as data (no fonts)
//   default-properties.css  registers the tokens with @property (about 110 kB, no effect that we use)
//   default-container-properties.css  the same for the container sizes
//   colors/, density/, fonts/ and helpers/ classes   no page uses them yet
const foundationFiles = [
  "defaults/default-required.css",
  "defaults/default-variables.css",
  "defaults/default-container-variables.css",
  "defaults/default-root.css",
  "defaults/default-elevation.css",
  "defaults/default-fonts.css",
  "defaults/default-code.css",
];

// Components that the pages use or will use. The component folder holds one style sheet per
// component. Add a name here and run make ui-css to ship another component.
const componentNames = [
  "button", "notification", "tag", "badge", "table", "table-row", "table-head", "table-header-cell",
  "table-data-cell", "stack", "card", "section", "divider", "link", "infotext",
  "input", "select", "textarea", "checkbox",
  "navigation", "navigation-item", "tabs", "tab-list", "tab-item", "tab-panel",
];
const componentFiles = componentNames.map((n) => `components/${n}/${n}.css`);
// Shared parts of the components.
const sharedFiles = ["styles/component-animations.css", "styles/visually-hidden.css"];

// Custom properties that no style sheet defines. The scripts of the web components set them on an
// element (table columns, drawer width) or they hold an image that we do not ship (scrollbar
// buttons, textarea resize handle). A rule that reads one of them is then ignored by the browser,
// which is what we want. Any other undefined property fails the build.
const allowedUndefined = new Set([
  "--db-color-scheme",
  "--db-table-columns",
  "--db-drawer-max-width-horizontal",
  "--db-textarea-resizer-image",
  "--db-scrollbar-button-vertical-decrement",
  "--db-scrollbar-button-vertical-increment",
  "--db-scrollbar-button-horizontal-decrement",
  "--db-scrollbar-button-horizontal-increment",
]);

function read(base, rel) {
  const file = path.join(base, rel);
  if (!fs.existsSync(file)) throw new Error(`missing in the package: ${file}`);
  return fs.readFileSync(file, "utf8");
}

// minify removes comments and white space that has no meaning. It keeps the spaces that CSS needs
// (between selectors, around + and - in calc()).
function minify(css) {
  return css
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\s+/g, " ")
    .replace(/ ?([{};,]) ?/g, "$1")
    .replace(/([:(]) /g, "$1")
    .replace(/ \)/g, ")")
    .replace(/;}/g, "}")
    .trim();
}

function packageInfo(name) {
  return JSON.parse(fs.readFileSync(path.join(modules, name, "package.json"), "utf8"));
}

const parts = [];
for (const f of foundationFiles) parts.push(minify(read(foundations, f)));
for (const f of componentFiles) parts.push(minify(read(components, f)));
for (const f of sharedFiles) parts.push(minify(read(path.join(components), f)));
parts.push(minify(read(__dirname, "theme.css")));
const body = parts.join("\n");

// The result must carry no font data and no address that the page would have to load.
for (const [what, re] of [["@font-face", /@font-face/], ["url()", /url\(/], ["@import", /@import/], ["base64", /base64/]]) {
  if (re.test(body)) throw new Error(`the style sheet contains ${what}; the pages allow no fonts and no external files`);
}

// Every custom property that a rule reads without a fallback must be defined somewhere.
const defined = new Set([...body.matchAll(/(--[a-z0-9-]+):/g)].map((m) => m[1]));
const missing = new Set();
for (const m of body.matchAll(/var\((--[a-z0-9-]+)([,)])/g)) {
  if (m[2] === ")" && !defined.has(m[1]) && !allowedUndefined.has(m[1])) missing.add(m[1]);
}
if (missing.size > 0) throw new Error(`custom properties without a definition: ${[...missing].join(", ")}`);

const fnd = packageInfo("core-foundations");
const cmp = packageInfo("core-components");
for (const p of [fnd, cmp]) {
  if (p.license !== "Apache-2.0") throw new Error(`${p.name} is licensed ${p.license}, not Apache-2.0: review the notice`);
}
const banner =
  "/* Generated by scripts/ui-css; do not edit. Run: make ui-css.\n" +
  ` Contains parts of ${fnd.name} ${fnd.version} and ${cmp.name} ${cmp.version} (DB UX Design System version 3),\n` +
  " Copyright (c) DB Systel GmbH, Apache License 2.0. Changed: selected components, no fonts or icons, minified,\n" +
  " own theme added. No design assets of Deutsche Bahn. Full notice: third-party-notices.txt in the folder of this file. */\n";
const css = banner + body + "\n";

const packages = [fnd, cmp].map((p) => `  ${p.name} ${p.version}  (${p.license})`).join("\n");
const notice =
  read(__dirname, "notice-header.txt").replace("@PACKAGES@", packages) + read(__dirname, "LICENSE-Apache-2.0.txt");

const targets = ["hub/stage/web/embed", "hub/portal/web/embed"];
for (const t of targets) {
  fs.writeFileSync(path.join(root, t, "base.css"), css);
  fs.writeFileSync(path.join(root, t, "third-party-notices.txt"), notice);
}

const { gzipSync } = require("node:zlib");
console.log(`base.css: ${Buffer.byteLength(css)} bytes, ${gzipSync(css).length} bytes gzip (written to ${targets.join(" and ")})`);
