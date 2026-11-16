# Changelog

## [0.2.0](https://github.com/mrsimpson/agentic-office/compare/v0.1.2...v0.2.0) (2026-11-16)


### ⚠ BREAKING CHANGES

* data of a hub that was created before this change is not supported, and there is no migration. Start with an empty data directory. The artifact repository of a tenant is a bare repository that starts with a first commit; the session logs lie in a second repository per tenant; every stored YAML file names its schema in a schema field; slides no longer store the rendered HTML; the answer to the sign-in with a key carries the display name of the participant.

### Features

* artifacts reach the participant's computer through Git (dec-artifact-sync) ([#145](https://github.com/mrsimpson/agentic-office/issues/145)) ([7dba1c9](https://github.com/mrsimpson/agentic-office/commit/7dba1c9d9587607cf1cf36bb0836c52a91095c26))
* **docs:** add llms.txt and a setup guide for agents; link back from the architecture ([#143](https://github.com/mrsimpson/agentic-office/issues/143)) ([800a32d](https://github.com/mrsimpson/agentic-office/commit/800a32d57270db6dc5342d6ac15df0255aa7e807))
* **docs:** publish a documentation site with the user guide, the command reference and the architecture ([#141](https://github.com/mrsimpson/agentic-office/issues/141)) ([380605c](https://github.com/mrsimpson/agentic-office/commit/380605c81730ad65b9e173bdc51b31e4d728b39f))


### Documentation

* **architecture:** accept dec-hub-profiles; a contract test uses the protocol of its interface ([#137](https://github.com/mrsimpson/agentic-office/issues/137)) ([475395b](https://github.com/mrsimpson/agentic-office/commit/475395bdf4cbc93da3ba73cc945a7bb5f09c571b)), closes [#131](https://github.com/mrsimpson/agentic-office/issues/131) [#134](https://github.com/mrsimpson/agentic-office/issues/134)
* **architecture:** keep decisions and concepts at the level of responsibility; add the arc42-language skill ([#144](https://github.com/mrsimpson/agentic-office/issues/144)) ([217b1e6](https://github.com/mrsimpson/agentic-office/commit/217b1e6c16c408dffd068d5e2143b9cadbf0105b))

## [0.1.2](https://github.com/mrsimpson/agentic-office/compare/v0.1.1...v0.1.2) (2026-11-15)


### Features

* **client:** hub, tenant and certificate in config.yaml, named hub profiles ([#132](https://github.com/mrsimpson/agentic-office/issues/132)) ([556cf78](https://github.com/mrsimpson/agentic-office/commit/556cf78059ea5a3147d87b78a76b6985c03e8723)), closes [#130](https://github.com/mrsimpson/agentic-office/issues/130)


### Documentation

* slim the README, move process and configuration to the user guide, explain every command with --help ([#135](https://github.com/mrsimpson/agentic-office/issues/135)) ([d605626](https://github.com/mrsimpson/agentic-office/commit/d6056268557e324cafa8a2b0ab979483cb087e69))

## [0.1.1](https://github.com/mrsimpson/agentic-office/compare/v0.1.0...v0.1.1) (2026-11-14)


### Features

* **hub:** add GET /healthz for container probes ([#128](https://github.com/mrsimpson/agentic-office/issues/128)) ([bb4e96c](https://github.com/mrsimpson/agentic-office/commit/bb4e96cd02c34b33edb3a544de069dbc7009ca18))

## [0.1.0](https://github.com/mrsimpson/agentic-office/compare/v0.0.4...v0.1.0) (2026-11-13)


### Features

* semantic versioning from conventional commits ([#123](https://github.com/mrsimpson/agentic-office/issues/123)) ([d671a8c](https://github.com/mrsimpson/agentic-office/commit/d671a8cf27ded3befa63d59a347fcd038549a0ab))
