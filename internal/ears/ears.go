// Package ears is the spike of the ears (bb-ears): audio -> VAD -> Whisper -> utterance.submit.
// Only text leaves this package. Raw audio is never sent.
package ears

import (
	"context"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	officev1 "github.com/mrsimpson/agentic-office/gen/office/v1"
	"github.com/mrsimpson/agentic-office/gen/office/v1/officev1connect"
	"github.com/mrsimpson/agentic-office/internal/metrics"
)

// Ears connects one audio source to one participant in one session.
type Ears struct {
	Client        officev1connect.SessionServiceClient
	Session       string
	ParticipantID string
	Transcriber   *Transcriber
	VAD           VAD
	Status        io.Writer // the speaker's terminal
}

type utterance struct {
	commandID     string
	text          string
	transcribedAt time.Time
}

// Run processes frames until the source closes, then sends all queued utterances.
func (e *Ears) Run(ctx context.Context, frames <-chan Frame) error {
	segs := make(chan *Segment, 16)
	queue := make(chan utterance, 1024) // local queue: text only, in order
	sendDone := make(chan error, 1)
	go func() { sendDone <- e.sendLoop(ctx, queue) }()
	go func() {
		defer close(queue)
		for seg := range segs {
			start := time.Now()
			text, err := e.Transcriber.Transcribe(seg.Samples)
			done := time.Now()
			if err != nil {
				fmt.Fprintf(e.Status, "ears: whisper failed: %v\n", err)
				continue
			}
			audio := time.Duration(len(seg.Samples)) * time.Second / SampleRate
			metrics.Emit("whisper", map[string]any{
				"audio_ms": audio.Milliseconds(), "whisper_ms": done.Sub(start).Milliseconds(),
				"end_of_speech_to_text_ms": done.Sub(seg.EndedAt).Milliseconds(),
			})
			if text == "" {
				continue
			}
			u := utterance{commandID: uuid.Must(uuid.NewV7()).String(), text: text, transcribedAt: done}
			fmt.Fprintf(e.Status, "final   %q\n", text)
			queue <- u
		}
	}()

	// Frame loop. It also measures how regularly frames arrive while Whisper runs.
	var last time.Time
	var gaps []time.Duration
	var lastAt time.Time
	for f := range frames {
		if !last.IsZero() {
			gaps = append(gaps, f.At.Sub(last))
		}
		last, lastAt = f.At, f.At
		if seg := e.VAD.Push(f); seg != nil {
			segs <- seg
		}
	}
	if seg := e.VAD.Flush(lastAt); seg != nil {
		segs <- seg
	}
	close(segs)
	reportJitter(gaps)
	return <-sendDone
}

// sendLoop sends utterances in order. It repeats a failed call with the same command ID
// until it succeeds, so a drop never creates a duplicate (dec-idempotent-commands).
func (e *Ears) sendLoop(ctx context.Context, queue <-chan utterance) error {
	for u := range queue {
		backoff := 100 * time.Millisecond
		for attempt := 1; ; attempt++ {
			res, err := e.Client.SubmitUtterance(ctx, &officev1.SubmitUtteranceRequest{
				SessionId: e.Session, CommandId: u.commandID, ParticipantId: e.ParticipantID,
				Text: u.text, Modality: officev1.Modality_MODALITY_VOICE,
				TranscribedAt: timestamppb.New(u.transcribedAt),
			})
			if err == nil {
				ev := res.GetEvent()
				metrics.Emit("submit", map[string]any{
					"event_id": ev.GetId(), "attempts": attempt, "duplicate": res.GetDuplicate(),
					"transcribed_to_appended_us": ev.GetTime().AsTime().Sub(u.transcribedAt).Microseconds(),
					"transcribed_to_ack_us":      time.Since(u.transcribedAt).Microseconds(),
				})
				fmt.Fprintf(e.Status, "sent    #%d %q\n", ev.GetId(), u.text)
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(e.Status, "queued  %q (%v), retry in %v\n", u.text, err, backoff)
			time.Sleep(backoff)
			backoff = min(backoff*2, 2*time.Second)
		}
	}
	return nil
}

func reportJitter(gaps []time.Duration) {
	if len(gaps) == 0 {
		return
	}
	dev := make([]time.Duration, len(gaps))
	for i, g := range gaps {
		d := g - frameDur
		if d < 0 {
			d = -d
		}
		dev[i] = d
	}
	slices.Sort(dev)
	pick := func(p float64) int64 { return dev[min(len(dev)-1, int(p*float64(len(dev))))].Microseconds() }
	metrics.Emit("frame_jitter", map[string]any{
		"frames": len(gaps), "p50_us": pick(0.5), "p99_us": pick(0.99), "max_us": dev[len(dev)-1].Microseconds(),
	})
}
