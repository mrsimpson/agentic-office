// Package control is the interface if-sync of the building block Artifact sync: set up a directory
// as a clone of the artifact repository of a tenant, and answer Git when it asks for a password
// (dec-artifact-sync).
//
// The clone lives in a directory that the participant sets up on purpose. The sync runs no other
// Git command for the participant than clone and update. The later steps add the update and the
// follow of the changes to this interface.
//
// The wiring of the participant client hands a New function to the CLI. The CLI runs the sync as
// its own process, like the replication.
package control
