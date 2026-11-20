package sessionimportv1

import (
	"crypto/sha256"
	"encoding/binary"
)

// ImportedPrefix starts the participant ID of every imported participant in the session: the ID is
// ImportedPrefix + key. No registered participant ID contains ':', so an imported participant
// never matches a registered one.
const ImportedPrefix = "imported:"

// ImportDigest returns the digest of the imported utterances of a session after one more
// utterance: prev is the digest of the utterances before it (empty for none). The base of a page
// (ImportBase) carries the digest of all imported utterances, so the hub and the client compute it
// the same way, each from its own copy: the hub from the session log, the client from its file.
//
// The digest covers the speaker key, the text, the modality and the original time of each
// utterance, in order. It is SHA-256 over the previous digest and the four values, each with
// its length in front, so no two different lists of utterances share an encoding.
func ImportDigest(prev []byte, u *ImportedUtterance) []byte {
	h := sha256.New()
	field := func(b []byte) {
		h.Write(binary.AppendUvarint(nil, uint64(len(b))))
		h.Write(b)
	}
	field(prev)
	field([]byte(u.GetSpeaker()))
	field([]byte(u.GetText()))
	field(binary.AppendVarint(nil, int64(u.GetModality())))
	var t []byte
	if ts := u.GetTime(); ts != nil {
		t = binary.AppendVarint(binary.AppendVarint(nil, ts.GetSeconds()), int64(ts.GetNanos()))
	}
	field(t)
	return h.Sum(nil)
}
