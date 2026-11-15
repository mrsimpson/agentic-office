// Settings that the Astro configuration and the build scripts share.

// GitHub Pages serves the repository at https://<owner>.github.io/<repo>/.
export const origin = "https://mrsimpson.github.io";
export const base = "/agentic-office";
export const repository = "https://github.com/mrsimpson/agentic-office";
export const branch = "main";

// The architecture is a section of the site: the output of "arc42 build", placed under this path.
export const architecturePath = "architecture";

// Same version as in the Makefile and in tests/architecture/check.go.
export const arc42 = ["-y", "@doctc/arc42@0.38.0"];

// Files for agents, from docs/site/agent/ to the root of the site. In them, {{site}} stands for the
// address of the site, because agents need absolute links.
export const agentFiles = ["llms.txt", "agent-setup.md"];
