# Changelog

## [0.4.5](https://github.com/mrsimpson/agentic-office/compare/v0.4.4...v0.4.5) (2026-11-27)


### Features

* **client:** open the stage in the browser after join ([#292](https://github.com/mrsimpson/agentic-office/issues/292)) ([b84fc18](https://github.com/mrsimpson/agentic-office/commit/b84fc1873f769f9a07efbf4a504d623192e00c68)), closes [#291](https://github.com/mrsimpson/agentic-office/issues/291)
* **ears:** voice print for loudspeakers and an overlap hint ([#289](https://github.com/mrsimpson/agentic-office/issues/289)) ([ec60d9c](https://github.com/mrsimpson/agentic-office/commit/ec60d9cb8367ab80d4ac8e35f7692cb0a55d3a02))

## [0.4.4](https://github.com/mrsimpson/agentic-office/compare/v0.4.3...v0.4.4) (2026-11-26)


### Features

* **agentrunner:** instruct agents how to take part in a conversation ([#287](https://github.com/mrsimpson/agentic-office/issues/287)) ([fba76c0](https://github.com/mrsimpson/agentic-office/commit/fba76c09d3f32bb41d725e2bdcf98158d7e4a355))

## [0.4.3](https://github.com/mrsimpson/agentic-office/compare/v0.4.2...v0.4.3) (2026-11-25)


### Features

* run a team agent without a human with a key of its own ([#284](https://github.com/mrsimpson/agentic-office/issues/284)) ([6a4d885](https://github.com/mrsimpson/agentic-office/commit/6a4d885373a8dab643b6d2806af685d43ea29604)), closes [#278](https://github.com/mrsimpson/agentic-office/issues/278)
* **session,portal:** trim and soft-delete an ended session ([#283](https://github.com/mrsimpson/agentic-office/issues/283)) ([56b40b8](https://github.com/mrsimpson/agentic-office/commit/56b40b801648c8674f6974461cf47315996ae857))
* team agents with a token as the agent and a shared definition file ([#281](https://github.com/mrsimpson/agentic-office/issues/281)) ([5c27ad0](https://github.com/mrsimpson/agentic-office/commit/5c27ad032e46819c2857911fcd65377057ac027c))


### Documentation

* **architecture:** add runtime scenarios for team agents; stop the runner of an ended team agent ([#282](https://github.com/mrsimpson/agentic-office/issues/282)) ([dfedc0d](https://github.com/mrsimpson/agentic-office/commit/dfedc0d8ed72f40fc4028c1ee2c8a1a439eecf17)), closes [#278](https://github.com/mrsimpson/agentic-office/issues/278)
* **architecture:** propose team agents with a shared identity and no shared key ([#279](https://github.com/mrsimpson/agentic-office/issues/279)) ([5b8ac54](https://github.com/mrsimpson/agentic-office/commit/5b8ac546332090d4d75c17ba76e7c6b867d87642))
* **architecture:** state each fact once, in its owner element ([#285](https://github.com/mrsimpson/agentic-office/issues/285)) ([439496e](https://github.com/mrsimpson/agentic-office/commit/439496e1c597381167971cfe996e628b0fee54dc))

## [0.4.2](https://github.com/mrsimpson/agentic-office/compare/v0.4.1...v0.4.2) (2026-11-24)


### Features

* **agentrunner:** agents present an artifact on the stage after they worked on it ([#275](https://github.com/mrsimpson/agentic-office/issues/275)) ([6d3614b](https://github.com/mrsimpson/agentic-office/commit/6d3614b2989b86c67bb3549a582ceccc38ecc63a)), closes [#273](https://github.com/mrsimpson/agentic-office/issues/273)
* **stage:** tabs open an artifact in the own browser; a new shared focus pulses ([#276](https://github.com/mrsimpson/agentic-office/issues/276)) ([e4981e5](https://github.com/mrsimpson/agentic-office/commit/e4981e59b0838db6e9f9d32ea8ebd516af13d39c)), closes [#274](https://github.com/mrsimpson/agentic-office/issues/274)


### Bug fixes

* **client,hub:** honor XDG_CONFIG_HOME on every system and run the tests on macOS ([#269](https://github.com/mrsimpson/agentic-office/issues/269)) ([a0a7fed](https://github.com/mrsimpson/agentic-office/commit/a0a7feda52565d1b3ac77fd660bbcf88aa8da856)), closes [#266](https://github.com/mrsimpson/agentic-office/issues/266)
* **client:** restart a failed harness and deliver again what it missed ([#272](https://github.com/mrsimpson/agentic-office/issues/272)) ([ecd82fb](https://github.com/mrsimpson/agentic-office/commit/ecd82fb8bd227e95e4e50a1021f9ed02f18e8fac)), closes [#271](https://github.com/mrsimpson/agentic-office/issues/271)

## [0.4.1](https://github.com/mrsimpson/agentic-office/compare/v0.4.0...v0.4.1) (2026-11-22)


### Features

* **stage:** participants as small signs in the header, a transcript that scrolls ([#264](https://github.com/mrsimpson/agentic-office/issues/264)) ([a486af7](https://github.com/mrsimpson/agentic-office/commit/a486af71c76fea4eb5c7b55e85e3441038758b0a))


### Bug fixes

* **stage:** show the staged artifacts of the session on its stage ([#268](https://github.com/mrsimpson/agentic-office/issues/268)) ([c8ba062](https://github.com/mrsimpson/agentic-office/commit/c8ba062a3531c97cbae149c18d9ba1c1c43227fc)), closes [#267](https://github.com/mrsimpson/agentic-office/issues/267)

## [0.4.0](https://github.com/mrsimpson/agentic-office/compare/v0.3.1...v0.4.0) (2026-11-20)


### Features

* **cli,stage:** print the stage link after join and link the stage to the portal ([#260](https://github.com/mrsimpson/agentic-office/issues/260)) ([a99dafb](https://github.com/mrsimpson/agentic-office/commit/a99dafb7765d9301a27d8574201f6ba27a462ae8))
* **import:** extend an imported session with a longer version of the same conversation ([#258](https://github.com/mrsimpson/agentic-office/issues/258)) ([5c14e90](https://github.com/mrsimpson/agentic-office/commit/5c14e902ce5aa0e02bcfc1a885ec963be916968d)), closes [#257](https://github.com/mrsimpson/agentic-office/issues/257) [#51](https://github.com/mrsimpson/agentic-office/issues/51)

## [0.3.1](https://github.com/mrsimpson/agentic-office/compare/v0.3.0...v0.3.1) (2026-11-20)


### Bug fixes

* **portal:** send the origin with forms, so a browser can release a session ([#255](https://github.com/mrsimpson/agentic-office/issues/255)) ([82807df](https://github.com/mrsimpson/agentic-office/commit/82807df728da57d4a1a1a64c6fda873c5d0c683b)), closes [#254](https://github.com/mrsimpson/agentic-office/issues/254)


### Chores

* **release:** record 0.3.0, which was tagged without the release pull request ([#256](https://github.com/mrsimpson/agentic-office/issues/256)) ([6e28ea3](https://github.com/mrsimpson/agentic-office/commit/6e28ea30932f9afeb314686dc7b60688543b046d))

## [0.3.0](https://github.com/mrsimpson/agentic-office/compare/v0.2.0...v0.3.0) (2026-11-20)


### ⚠ BREAKING CHANGES

* office meeting events and every read of a session by a member who never joined it fail with not_found.
* office admin link, /admin/ and CreateAdminLink/ExchangeAdminLink are gone; use office portal link, /portal/ and CreatePortalLink/ExchangePortalLink.

### Features

* **agentrunner:** ask the agent once when its reply stayed in its chat ([#175](https://github.com/mrsimpson/agentic-office/issues/175)) ([c305c23](https://github.com/mrsimpson/agentic-office/commit/c305c23552744ce333dead9a15260ab9b7386ca1)), closes [#170](https://github.com/mrsimpson/agentic-office/issues/170)
* **agentrunner:** hand the office-memory skill to the harness ([#222](https://github.com/mrsimpson/agentic-office/issues/222)) ([ba60f33](https://github.com/mrsimpson/agentic-office/commit/ba60f33f388ef3884460ceedfa3c8f58c6956d32))
* **archive:** a staging repository per tenant ([#211](https://github.com/mrsimpson/agentic-office/issues/211)) ([2e007d6](https://github.com/mrsimpson/agentic-office/commit/2e007d64c81cc6354442f8c9883741c7f212b2ea)), closes [#199](https://github.com/mrsimpson/agentic-office/issues/199)
* **archive:** discard a staged artifact that the release kept back ([#237](https://github.com/mrsimpson/agentic-office/issues/237)) ([a188536](https://github.com/mrsimpson/agentic-office/commit/a188536cbb47df56b6e3dfdfd71532cc9bf79bda))
* **artifacts:** each type describes its files for the wiki ([#229](https://github.com/mrsimpson/agentic-office/issues/229)) ([5137616](https://github.com/mrsimpson/agentic-office/commit/513761696d596da7668bf847110ee7ab28cdc3b0))
* **cli:** help for command groups and suggestions for unknown commands ([#184](https://github.com/mrsimpson/agentic-office/issues/184)) ([10a15f0](https://github.com/mrsimpson/agentic-office/commit/10a15f0ae39ff5b0ddc4089e5c6baf31018fd073)), closes [#183](https://github.com/mrsimpson/agentic-office/issues/183)
* **documents:** front matter schema and hints for broken links ([#224](https://github.com/mrsimpson/agentic-office/issues/224)) ([dccf2bf](https://github.com/mrsimpson/agentic-office/commit/dccf2bf59c18897d5ef6eb6ef9e451cac46ed220)), closes [#205](https://github.com/mrsimpson/agentic-office/issues/205)
* **gitaccess:** push rules protect stubs; a check for documents ([#221](https://github.com/mrsimpson/agentic-office/issues/221)) ([a1f2fd8](https://github.com/mrsimpson/agentic-office/commit/a1f2fd87400be2391a4fec108749338cce0b53c4))
* list the sessions that a user may see, in the portal and the CLI (if-session-list) ([#185](https://github.com/mrsimpson/agentic-office/issues/185)) ([bca2e70](https://github.com/mrsimpson/agentic-office/commit/bca2e708522d8ec236b8cd5295d92bc81ed5f178))
* only participants, the importer, tenant admins and stage links read a session ([#189](https://github.com/mrsimpson/agentic-office/issues/189)) ([6dedccd](https://github.com/mrsimpson/agentic-office/commit/6dedccdbb74fc73c7f8962e12d5da687b2ce84ec)), closes [#187](https://github.com/mrsimpson/agentic-office/issues/187) [#179](https://github.com/mrsimpson/agentic-office/issues/179)
* **portal:** read one session: minutes, decisions, tasks, notes and transcript ([#191](https://github.com/mrsimpson/agentic-office/issues/191)) ([3b0a9a1](https://github.com/mrsimpson/agentic-office/commit/3b0a9a1eb46bdc1fadc5d15726799e15b7ff1b80)), closes [#188](https://github.com/mrsimpson/agentic-office/issues/188) [#179](https://github.com/mrsimpson/agentic-office/issues/179)
* **portal:** release status, staged artifacts, release and objection; refuse quotes off the record ([#223](https://github.com/mrsimpson/agentic-office/issues/223)) ([6550891](https://github.com/mrsimpson/agentic-office/commit/655089108a59371a348e74c88c696665bbcf9e3b))
* **portal:** show the wiki of a tenant with index, references and search ([#236](https://github.com/mrsimpson/agentic-office/issues/236)) ([8c12b47](https://github.com/mrsimpson/agentic-office/commit/8c12b473918ed4beb0a3aaff6c173b4f15ffec07)), closes [#208](https://github.com/mrsimpson/agentic-office/issues/208)
* **records:** staged minutes, notes and decisions, with stubs ([#216](https://github.com/mrsimpson/agentic-office/issues/216)) ([0a655eb](https://github.com/mrsimpson/agentic-office/commit/0a655eb6ee6e831bc1dd21a673556d6280af19df))
* **records:** store notes as Markdown with front matter and tags ([#210](https://github.com/mrsimpson/agentic-office/issues/210)) ([62a0b21](https://github.com/mrsimpson/agentic-office/commit/62a0b211640cea5db029c3539882ee0902e6537e))
* **session:** release status, release and objection, durable deadlines ([#214](https://github.com/mrsimpson/agentic-office/issues/214)) ([88a093a](https://github.com/mrsimpson/agentic-office/commit/88a093a7a0625921e2a26b3f9f79cd45721aa566))
* show the activity of agents on the stage (dec-agent-activity) ([#178](https://github.com/mrsimpson/agentic-office/issues/178)) ([3516c9e](https://github.com/mrsimpson/agentic-office/commit/3516c9ee39c2bd1c07552aeb2fd26fec28431944))
* stage documents and slide decks of a session, with stubs ([#218](https://github.com/mrsimpson/agentic-office/issues/218)) ([3771ff0](https://github.com/mrsimpson/agentic-office/commit/3771ff0ca9f054a6eeb71a5a33178d98efe1dc75))
* the portal replaces the admin view (dec-web-portal) ([#181](https://github.com/mrsimpson/agentic-office/issues/181)) ([2938f69](https://github.com/mrsimpson/agentic-office/commit/2938f690d67f977b30d1186cc4f1499d546ff89e))
* **wiki:** give the trust data of an artifact to the portal ([#247](https://github.com/mrsimpson/agentic-office/issues/247)) ([3af7d98](https://github.com/mrsimpson/agentic-office/commit/3af7d9804e525f4f8cb29a654f03e67416adc7e0)), closes [#235](https://github.com/mrsimpson/agentic-office/issues/235)
* **wiki:** wiki part with index, backlinks, broken links, search and ingest queue ([#234](https://github.com/mrsimpson/agentic-office/issues/234)) ([3fb9ed0](https://github.com/mrsimpson/agentic-office/commit/3fb9ed09d5791cc4e706f01af3616d15e8344951))


### Bug fixes

* **archive:** release replaces a path only where it holds the recorded stub ([#233](https://github.com/mrsimpson/agentic-office/issues/233)) ([9284ebd](https://github.com/mrsimpson/agentic-office/commit/9284ebd65476d554dbba3b20e1d38eb075bc3755))
* **hub:** check the start before writing, refuse a certificate that misses a host ([#186](https://github.com/mrsimpson/agentic-office/issues/186)) ([503e0d9](https://github.com/mrsimpson/agentic-office/commit/503e0d959f00169d66de640043efa236b0547c59)), closes [#182](https://github.com/mrsimpson/agentic-office/issues/182)
* **session:** refuse a release because of quotes with an error ([#232](https://github.com/mrsimpson/agentic-office/issues/232)) ([03fb05d](https://github.com/mrsimpson/agentic-office/commit/03fb05d3dcd030a16add25ef8caae9a314d7cd9f)), closes [#230](https://github.com/mrsimpson/agentic-office/issues/230)


### Documentation

* add the office-assistant skill ([3b9eb4d](https://github.com/mrsimpson/agentic-office/commit/3b9eb4ddd2fb724cb19fa763f0be3f7abc153b34))
* **architecture:** a wiki of the tenant and the release of what a session creates ([#196](https://github.com/mrsimpson/agentic-office/issues/196)) ([c5f0357](https://github.com/mrsimpson/agentic-office/commit/c5f0357d6653193092a006c66bf6e94061bd1384))
* **architecture:** model the skills as a building block ([b6757aa](https://github.com/mrsimpson/agentic-office/commit/b6757aa63d918310a8e3ea4c42c9d87761e2bc9b))
* **architecture:** name the hints in the CLI and the MCP tools ([#225](https://github.com/mrsimpson/agentic-office/issues/225)) ([8098fd7](https://github.com/mrsimpson/agentic-office/commit/8098fd76b82aa6a53695ea99ffde89bcb50cf296)), closes [#205](https://github.com/mrsimpson/agentic-office/issues/205)
* move the link example of the user guide into a code block ([#227](https://github.com/mrsimpson/agentic-office/issues/227)) ([5d7d1af](https://github.com/mrsimpson/agentic-office/commit/5d7d1af11cee2e2a52c0786de471bea160cff64d)), closes [#226](https://github.com/mrsimpson/agentic-office/issues/226)

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
