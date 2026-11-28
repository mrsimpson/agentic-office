# ui-css

Builds the base style sheet of the stage and the portal (`dec-ui-base`). It is development tooling,
like the docsite: Node.js is needed to run it, not to build the hub. The output is committed.

## Run

```
make ui-css
```

The script reads the pinned packages `@db-ux/core-foundations` and `@db-ux/core-components`
(DB UX Design System version 3, plain style sheets, `package.json` and `package-lock.json`) and
`theme.css`. It writes the same two files into each of the two web packages, because the stage and
the portal each serve their own copy and share no package:

- `hub/stage/web/embed/base.css` and `hub/portal/web/embed/base.css`
- `hub/stage/web/embed/third-party-notices.txt` and `hub/portal/web/embed/third-party-notices.txt`

Do not edit these files. Change `build.js` (the component list) or `theme.css`, run `make ui-css`
and commit the result.

## What goes in

- The tokens and the defaults of the elements from the foundations.
- The style sheet of each component in the list in `build.js`. To use another component, add its
  name to the list.
- `theme.css`: our own theme. It sets system fonts (the base defines no font family) and a neutral
  colour scheme in light and dark. A theme of the DB can replace this file later.

## What stays out

- Fonts and icons: no `@font-face`, no `url()`, no `base64` data. The build fails if one is left.
- The `@property` registrations and the colour, density and font classes. No page uses them.
- Every design asset of Deutsche Bahn. The code is Apache-2.0; the design assets have a separate
  licence that does not allow us to ship them.

The build also fails when a rule reads a custom property that no style sheet defines (except a short
list of properties that the web components would set with scripts).

## Size

Measured with `make ui-css` (version 6.5.0, 25 components): 568,533 bytes raw, 49,227 bytes gzip,
for each copy. The page style sheets (`stage.css`, `portal.css`) come on top. The full bundle of
the components package is 1.36 MB raw.

## Licence

Both packages are licensed under the Apache License 2.0 and have no NOTICE file. The licence
requires that a recipient gets a copy of the licence and that changed files say so. Each
`base.css` starts with a comment that names the packages and the versions and says what changed,
and `third-party-notices.txt` (served next to the style sheet, at `/stage/assets/` and
`/portal/assets/`) holds the notice and the full text of the licence. `LICENSE-Apache-2.0.txt` and
`notice-header.txt` in this folder are the sources of that file.

## Cache

The page style sheets are served with a hash of their content in the file name, for example
`/stage/assets/base-0123456789ab.css`, with a cache lifetime of one year. A changed file has a new
address, so a browser never uses an old copy.
