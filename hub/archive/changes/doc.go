// Package changes is the interface if-artifact-changes of the building block Archive
// (docs/architecture/05-building-blocks.arc42.md). It tells the clients that follow a tenant which
// artifacts changed on the branch main, with their new versions (dec-artifact-sync).
//
// The contract is the Connect RPC ArchiveChangesService.Follow, a server stream. Its Protobuf
// contract and the generated code lie in the subdirectory api. The archive implements it in
// package archive (type ChangesService), one instance for each tenant.
//
// How it works:
//
//   - After every change of main, whether the archive committed it or a push moved it (see
//     package repository), the archive compares the old and the new tree and sends one event
//     to every follower of the tenant. An event lists the artifacts that were created,
//     changed or removed, with the new version (the Git object ID of the content, the same
//     version as in if-archive-read). An event holds no content and no position. There is no
//     cursor and no replay.
//   - Only Markdown (.md) and YAML (.yaml, .yml) files are named. A change without such a
//     file gives no event. The session logs lie in their own repository and never give an event.
//   - The caller needs the access token of a participant who belongs to the tenant. A caller
//     who is no member gets the answer for a tenant that does not exist; a stage link cannot
//     call. The caller may name path prefixes and then only gets artifacts under them.
//   - The commit path never waits for a follower. The archive puts each event into a small
//     buffer of the follower (16 events) without blocking. A follower whose buffer is full
//     is dropped: its stream fails with resource_exhausted and it connects again. A follower
//     whose client has gone is removed when its call ends. A slow or gone follower never holds
//     up a commit or another follower.
//   - The first message of a call is a signal of its own type, Registered. It carries no
//     changes. The hub sends it when it has registered the follower, so a client that has
//     received it gets every later change. Every later message carries changes (ArtifactChanges,
//     at least one entry). A client that wants a complete clone connects, waits for the signal
//     and then updates its clone through Git.
package changes
