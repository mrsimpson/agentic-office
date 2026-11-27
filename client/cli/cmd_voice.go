package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

// voicePrintEnv is the voice print file of OFFICE_VOICE_PRINT, or "" for the default of the ears.
func voicePrintEnv() string { return os.Getenv("OFFICE_VOICE_PRINT") }

// VoiceEnrolOptions are the options of "office voice enrol".
type VoiceEnrolOptions struct {
	// WAV is a recording of the owner to use instead of the microphone.
	WAV string
	// Out is the voice print file. Empty means the default file of the ears.
	Out string
	// VADModel is the path of the VAD model.
	VADModel string
	// Seconds is the speech to collect.
	Seconds int
	// Replace overwrites an existing voice print.
	Replace bool
}

// VoicePrintResult is the result of "office voice enrol".
type VoicePrintResult struct {
	File    string  `json:"file"`
	Seconds float64 `json:"seconds"`
	Model   string  `json:"model"`
}

// VoicePrintInfo is the result of "office voice show".
type VoicePrintInfo struct {
	File      string    `json:"file"`
	Exists    bool      `json:"exists"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitzero"`
	Seconds   float64   `json:"seconds,omitempty"`
}

// EnrolVoice, ShowVoicePrint and DeleteVoicePrint are the hooks for "office voice". Nil means the
// ears are not built in.
var (
	EnrolVoice       func(ctx context.Context, o VoiceEnrolOptions, progress func(string)) (VoicePrintResult, error)
	ShowVoicePrint   func(file string) (VoicePrintInfo, error)
	DeleteVoicePrint func(file string) (bool, error)
)

// cmdVoiceEnrol makes the voice print of the owner (dec-voice-print).
func cmdVoiceEnrol(e *env, fs *flag.FlagSet) func([]string) error {
	var o VoiceEnrolOptions
	fs.StringVar(&o.WAV, "wav", "", "use a recording of your voice instead of the microphone")
	fs.StringVar(&o.Out, "out", "", "voice print file (env OFFICE_VOICE_PRINT, default <data dir>/voiceprint.json)")
	fs.StringVar(&o.VADModel, "vad-model", "", "Silero VAD model (env OFFICE_VAD_MODEL)")
	fs.IntVar(&o.Seconds, "seconds", 20, "seconds of speech to collect (at least 10)")
	fs.BoolVar(&o.Replace, "replace", false, "replace an existing voice print")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if EnrolVoice == nil {
			return errEarsNotImplemented
		}
		if o.Out == "" {
			o.Out = voicePrintEnv()
		}
		r, err := EnrolVoice(e.ctx, o, e.progress)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(r)
			return nil
		}
		fmt.Fprintln(e.stdout, r.File)
		return nil
	}
}

// cmdVoiceShow says whether there is a voice print and what it was made of.
func cmdVoiceShow(e *env, fs *flag.FlagSet) func([]string) error {
	var file string
	fs.StringVar(&file, "file", "", "voice print file (env OFFICE_VOICE_PRINT, default <data dir>/voiceprint.json)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if ShowVoicePrint == nil {
			return errEarsNotImplemented
		}
		if file == "" {
			file = voicePrintEnv()
		}
		i, err := ShowVoicePrint(file)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(i)
			return nil
		}
		if !i.Exists {
			fmt.Fprintf(e.stdout, "no voice print at %s\n", i.File)
			return nil
		}
		fmt.Fprintf(e.stdout, "%s\nmade %s from %.0f seconds of speech, model %s\n",
			i.File, i.CreatedAt.Local().Format("2006-01-02 15:04"), i.Seconds, i.Model)
		return nil
	}
}

// cmdVoiceDelete removes the voice print.
func cmdVoiceDelete(e *env, fs *flag.FlagSet) func([]string) error {
	var file string
	fs.StringVar(&file, "file", "", "voice print file (env OFFICE_VOICE_PRINT, default <data dir>/voiceprint.json)")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		if DeleteVoicePrint == nil {
			return errEarsNotImplemented
		}
		if file == "" {
			file = voicePrintEnv()
		}
		had, err := DeleteVoicePrint(file)
		if err != nil {
			return err
		}
		if e.json {
			e.printJSON(map[string]any{"deleted": had})
			return nil
		}
		if had {
			e.progress("voice print deleted")
		} else {
			e.progress("there was no voice print")
		}
		return nil
	}
}
