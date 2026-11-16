// Package repository is the interface if-archive-repository of the building block Archive
// (docs/architecture/05-building-blocks.arc42.md). It lets Git access use the repository of a
// tenant (dec-artifact-sync). It is an in-process Go interface.
//
// Repositories says where Git finds the bare repository of a tenant (Dir) and takes the notice
// that main moved (MainMoved). Git access writes no file through it: Git changes the
// repository, and Git access reports the change afterwards.
//
// After MainMoved, the archive compares the state of main with the last state it knows and
// sends one event to the followers of the tenant (package changes). A change that the archive
// wrote itself gives the same event without a notice. The archive implements Repositories in
// package archive (type Repos).
package repository
