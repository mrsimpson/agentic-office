import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";
import starlightLinksValidator from "starlight-links-validator";
import { architecturePath, base, origin, repository } from "./site-config.mjs";

export default defineConfig({
  site: origin,
  base,
  integrations: [
    starlight({
      title: "Agentic Office",
      description:
        "A headless office for teams of humans and agents: user guide, command reference, developer documentation and architecture.",
      social: [{ icon: "github", label: "GitHub", href: repository }],
      sidebar: [
        { label: "User guide", items: [{ autogenerate: { directory: "user-guide" } }] },
        { label: "Command reference", items: [{ autogenerate: { directory: "reference" } }] },
        { label: "For developers", items: [{ autogenerate: { directory: "developers" } }] },
        // Not a Starlight page: the output of "arc42 build", added after the site build.
        { label: "Architecture", link: `/${architecturePath}/` },
      ],
      plugins: [
        // A broken link or anchor fails the build. The architecture is not part of the Astro site.
        starlightLinksValidator({
          errorOnRelativeLinks: false,
          exclude: [`${base}/${architecturePath}/`, `${base}/${architecturePath}/**`],
        }),
      ],
    }),
  ],
});
