package ears

/*
#include <whisper.h>
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

// languageCode returns the Whisper code of a language: "de" for "de", "german" or "German".
// "auto" stays "auto" (Whisper detects the language). The names are those of whisper.cpp.
func languageCode(lang string) (string, error) {
	l := strings.ToLower(strings.TrimSpace(lang))
	if l == "auto" {
		return l, nil
	}
	quiet()
	cs := C.CString(l)
	defer C.free(unsafe.Pointer(cs))
	id := C.whisper_lang_id(cs)
	if id < 0 {
		return "", fmt.Errorf("Whisper does not know the language %q; use a code such as de or en, or a name such as german", lang)
	}
	return C.GoString(C.whisper_lang_str(id)), nil
}
