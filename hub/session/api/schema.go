package sessionv1

// Schema is the schema of the session contract that this code implements (dec-session-schema).
// A session belongs to exactly one schema: the one it was started with. Its signed tree heads
// carry the schema (SignedTreeHead.schema), and the signature covers it.
//
// The form is "<Protobuf package>/<revision>". Increase the revision with every change of the
// events or of their encoding that changes a Merkle leaf (package merkle), for example a new field
// in an event. A new Protobuf package (office.session.v2) starts again at revision 1.
const Schema = "office.session.v1/1"
