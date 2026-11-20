// Package wiki is the building block Wiki (bb-wiki in
// docs/architecture/05-building-blocks.arc42.md). It realizes the wiki interface of the hub
// (if-wiki-api, WikiService in package api): the derived view of the wiki of one tenant
// (concept-wiki, dec-wiki).
//
// How it works:
//
//   - The part reads only. It follows the artifact repository of its tenant (if-archive-watch),
//     never the staging repository, and writes nothing. Open watches first and then reads every
//     file of the repository (if-archive-storage, storage.Root), so a change in between is not
//     lost. After each reported change it reads the changed files again. When the archive drops
//     the watcher because the part was too slow, the part watches again and reads every file
//     again. Nothing of the view is stored: a restart builds it again from the repository.
//   - The part reads no field of a type. It asks the artifact check (if-artifact-check,
//     check.Describe) for the description of each file: title, summary, tags, links, cited
//     sources, whether the file is a source and whether it is a stub. A file that is not
//     described (not valid, or of no type) and the stub of a staged artifact are not in the
//     index, the search or the ingest queue. Every file of the repository, also a stub, counts as
//     a target of a link.
//   - A link or a cited source leads somewhere when its path is a file of the repository or a
//     directory with files; otherwise it is broken.
//   - The ingest queue holds the sources (decisions, minutes, notes) that no artifact in the index
//     cites in its sources. Only documents cite sources.
//   - The search is an inverted index in memory with BM25 (k1 = 1.2, b = 0.75) over four fields
//     with weights: the title counts three times, the tags and the summary twice, the text once.
//     The text is the content of the file without the front matter of a Markdown file. A word
//     is a run of letters and digits, compared in lower case. The frequency of a word in an
//     artifact is the weighted sum over the fields, and the length of an artifact is the weighted
//     sum of the lengths of its fields. Hits are ordered by score, then by path.
//   - Every call needs the caller from the identity token check (if-token-check). The identity
//     interceptor has checked that the caller is a member of the tenant. A stage link, which has
//     no participant, is refused with PermissionDenied. Every member reads the whole view.
//   - Error codes: no valid token is Unauthenticated; a stage link is PermissionDenied; a missing
//     path or a query without a word is InvalidArgument; a path that is not in the index is
//     NotFound.
package wiki
