// Package identity is the building block Identity (bb-identity in docs/architecture/05-building-blocks.arc42.md).
//
// It knows users (participants), their public keys, their tenant memberships and the system admins,
// and issues short-lived access tokens. It provides if-auth-api (IdentityService), if-user-api
// (UserService) and if-token-check (Interceptor and CallerFrom).
//
// How it works:
//
//   - A participant has an ID chosen by a system admin (for example "ada"), a display name, a kind and
//     one or more Ed25519 public keys. This participant ID is also the participant ID in every
//     session: Join uses the participant of the token.
//   - Challenge and response: GetChallenge returns a random challenge that starts with
//     auth.ChallengePrefix and is valid for one exchange. ExchangeChallenge checks the Ed25519
//     signature and returns an opaque access token bound to the key. The lifetime is set by the
//     server flag -token-ttl (default 15 minutes).
//   - Tokens are kept in memory. A server restart drops all tokens, so clients authenticate again
//     when a call fails with "unauthenticated".
//   - Revoking a key ends its tokens at once (stronger than concept-identity requires).
//   - System admins: a user with the system admin role runs the hub (if-user-api). At the first
//     start the operator names the first one with -first-system-admin ID=PUBLIC_KEY. There is no
//     file of admin keys.
//   - Stage links: any participant can create a read-only link for one session. Its token allows
//     only SessionStreamService/Subscribe for that session, until it expires (-stage-link-ttl).
//   - Users, keys and memberships are YAML files in the hub repository (system/users and
//     system/memberships, through if-archive-storage). Identity is the only writer. Every change
//     and the loaded repository are checked against the rules of the user API (see user.proto);
//     the hub does not start when the repository breaks one. Tokens, challenges and the command
//     IDs are kept in memory.
//   - A user and a membership have a validity period. Removing ends it and keeps the record, so a
//     user ID is never used again.
//
// Error codes: a missing or bad field is InvalidArgument; a missing, unknown, expired or revoked
// token, an unknown or revoked key, and a wrong signature or challenge are Unauthenticated; a
// call that the token does not allow is PermissionDenied; a user that exists, a key that belongs to
// another user, or reusing a command_id for another request, is AlreadyExists; an unknown user,
// key or membership is NotFound; a change that breaks a rule of the user API is FailedPrecondition.
package identity
