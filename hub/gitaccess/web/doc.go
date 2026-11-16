// Package web is the interface if-git-access of the building block Git access: the repository of
// a tenant over HTTPS with the Git smart protocol (dec-artifact-sync).
//
// Address: the hub address, then PathPrefix, then the tenant. The repository of the tenant
// "acme" on the hub "https://hub.example.org:8740" is
//
//	https://hub.example.org:8740/git/acme
//
// Any Git client can use it: "git clone https://hub.example.org:8740/git/acme". The repository
// holds the artifacts of the tenant on the branch main and nothing else. The session logs lie in
// another repository that this interface never serves.
//
// Authentication: HTTP Basic. The user name is ignored. The password is the access token of the
// participant, as a Git credential helper hands it over. A request without a token, or with an
// unknown or expired token, gets 401 and the header "WWW-Authenticate: Basic", so that Git asks
// for credentials. A participant who is not a member of the tenant, the token of a stage link and
// a tenant that does not exist all get the same answer: 404 "not found".
//
// Operations: fetch and clone (the Git service git-upload-pack). A push (git-receive-pack) is
// refused with 403 in this step. Other addresses below the repository, such as the files of the
// dumb protocol, answer 404.
package web

// PathPrefix is where the repositories are mounted. The tenant follows it as the next path
// segment.
const PathPrefix = "/git/"
