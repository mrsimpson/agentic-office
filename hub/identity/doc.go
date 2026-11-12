// Package identity is the building block Identity (bb-identity in docs/architecture/05-building-blocks.arc42.md).
//
// It knows participants and their public keys and issues short-lived access tokens. It provides
// if-auth-api (IdentityService) and if-token-check (Interceptor and CallerFrom).
//
// How it works:
//
//   - A participant has an ID chosen by the admin (for example "ada"), a display name, a kind and
//     one or more Ed25519 public keys. This participant ID is also the participant ID in every
//     session: Join uses the participant of the token.
//   - Challenge and response: GetChallenge returns a random challenge that starts with
//     auth.ChallengePrefix and is valid for one exchange. ExchangeChallenge checks the Ed25519
//     signature and returns an opaque access token bound to the key. The lifetime is set by the
//     server flag -token-ttl (default 15 minutes).
//   - Tokens are kept in memory. A server restart drops all tokens, so clients authenticate again
//     when a call fails with "unauthenticated".
//   - Revoking a key ends its tokens at once (stronger than concept-identity requires).
//   - Admins: the keys in the admin file on the server host are admin keys. A token of an admin
//     key may call RegisterKey and RevokeKey. The file is read at start; see LoadAdmins. Keys of
//     the admin file cannot be revoked through the API: remove them from the file and restart.
//   - Stage links: any participant can create a read-only link for one session. Its token allows
//     only SessionStreamService/Subscribe for that session, until it expires (-stage-link-ttl).
//   - Registered participants and keys are stored in a JSON file (Options.StateFile) if one is
//     set. Tokens, challenges and the command IDs of admin commands are not stored.
//
// Error codes: a missing or bad field is InvalidArgument; a missing, unknown, expired or revoked
// token, an unknown or revoked key, and a wrong signature or challenge are Unauthenticated; a
// call that the token does not allow is PermissionDenied; registering a key that belongs to
// another participant, or reusing a command_id for another request, is AlreadyExists; an unknown
// key_id is NotFound; registering a revoked key again, or revoking a key of the admin file, is
// FailedPrecondition.
package identity

// The blank import states the required interface if-archive-storage (dec-multi-tenancy) until the
// first code that keeps users in the hub repository uses it.
import _ "github.com/mrsimpson/agentic-office/hub/archive/storage"
