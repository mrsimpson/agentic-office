package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mrsimpson/agentic-office/client/participant"
	"github.com/mrsimpson/agentic-office/client/replicator"
)

// How join runs the replicator:
//
//   - The replicator is its own process: "office replicator run". It holds a lock on the log
//     file, so there is at most one per session and participant. A join while one runs reuses it.
//   - Without --foreground, join starts it detached (its own process session, output to
//     <participant>.replicator.log) and returns as soon as the file is open, so that an agent in a
//     shell gets the path at once. The detached replicator stops on session.ended or when the
//     participant leaves.
//   - With --foreground (and with --mic or --wav), the replicator is a child of join and watches
//     it: it stops when join ends, also when join is killed (parent death).

type replicatorProc struct {
	pid int
	cmd *exec.Cmd // nil if the replicator was already running or is detached
	log string
}

// wait waits until the replicator stops or ctx ends. On ctx end, it stops the replicator.
func (r *replicatorProc) wait(ctx context.Context) error {
	if r.cmd == nil {
		// Not our child: poll until the lock is gone.
		for replicator.Running(r.log) {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- r.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		r.stop()
		<-done
		return nil
	}
}

func (r *replicatorProc) stop() {
	if r.cmd != nil && r.cmd.Process != nil {
		_ = r.cmd.Process.Signal(syscall.SIGTERM)
	}
}

// startReplicator makes sure a replicator runs for j. With foreground it is a child that stops
// when this process ends.
func startReplicator(e *env, j *participant.Joined, foreground bool) (*replicatorProc, error) {
	p := j.Paths
	if replicator.Running(p.Log) {
		return &replicatorProc{pid: readPID(p.PID), log: p.Log}, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	parent := 0
	if foreground {
		parent = os.Getpid()
	}
	rd, wr, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	cmd := exec.Command(exe, "replicator", "run", "--session", j.SessionID, "--participant", j.ParticipantID,
		"--log", p.Log, "--pid-file", p.PID, "--parent-pid", strconv.Itoa(parent), "--ready-fd", "3")
	cmd.Env = append(os.Environ(), e.cfg.Env()...)
	cmd.ExtraFiles = []*os.File{wr}
	if foreground {
		cmd.Stderr = os.Stderr
	} else {
		out, err := os.OpenFile(p.ReplicatorLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			wr.Close()
			return nil, err
		}
		defer out.Close()
		cmd.Stdout, cmd.Stderr = out, out
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	err = cmd.Start()
	wr.Close()
	if err != nil {
		return nil, err
	}
	line := make(chan string, 1)
	go func() {
		s, _ := bufio.NewReader(rd).ReadString('\n')
		line <- strings.TrimSpace(s)
	}()
	var msg string
	select {
	case msg = <-line:
	case <-time.After(15 * time.Second):
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, errors.New("the replicator did not start in time")
	}
	switch {
	case strings.HasPrefix(msg, "ready "):
		pid, _ := strconv.Atoi(strings.TrimPrefix(msg, "ready "))
		rp := &replicatorProc{pid: pid, log: p.Log}
		if foreground {
			rp.cmd = cmd
		} else {
			_ = cmd.Process.Release()
		}
		return rp, nil
	case msg == "running":
		// Another join started one at the same time.
		_ = cmd.Wait()
		return &replicatorProc{pid: readPID(p.PID), log: p.Log}, nil
	}
	_ = cmd.Wait()
	if msg == "" {
		msg = "it exited"
		if !foreground {
			msg += "; see " + p.ReplicatorLog
		}
	}
	return nil, fmt.Errorf("the replicator did not start: %s", strings.TrimPrefix(msg, "error "))
}

func readPID(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

// cmdReplicatorRun is the replicator process. join starts it; it is not meant to be called by
// hand.
func cmdReplicatorRun(e *env, fs *flag.FlagSet) func([]string) error {
	pid := fs.String("participant", "", "participant the log belongs to")
	path := fs.String("log", "", "local session log")
	pidFile := fs.String("pid-file", "", "file for the process ID")
	parent := fs.Int("parent-pid", 0, "stop when this process ends (0: never)")
	readyFD := fs.Int("ready-fd", 0, "file descriptor that gets one line when the log is open")
	return func(args []string) error {
		if err := noArgs(args); err != nil {
			return err
		}
		// --session is a common flag; it fills e.session.
		if e.session == "" || *pid == "" || *path == "" {
			return usagef("usage: office replicator run --session S --participant P --log FILE")
		}
		ready := func(string) {}
		if *readyFD > 0 {
			f := os.NewFile(uintptr(*readyFD), "ready")
			ready = func(s string) {
				fmt.Fprintln(f, s)
				f.Close()
			}
		}
		lg := log.New(e.stderr, "replicator: ", log.LstdFlags|log.Lmicroseconds)
		c, err := e.client()
		if err != nil {
			ready("error " + err.Error())
			return err
		}
		r := &replicator.Replicator{Session: c.Session, SessionID: e.session, ParticipantID: *pid, Path: *path,
			Logf: func(f string, a ...any) { lg.Printf(strings.TrimPrefix(f, "replicator: "), a...) }}
		if err := r.Open(); err != nil {
			if errors.Is(err, replicator.ErrRunning) {
				ready("running")
				return nil
			}
			ready("error " + err.Error())
			return err
		}
		if *pidFile != "" {
			if err := writePID(*pidFile); err != nil {
				r.Close()
				ready("error " + err.Error())
				return err
			}
			defer removePID(*pidFile)
		}
		lg.Printf("pid %d writes %s from cursor %d", os.Getpid(), *path, r.Cursor())
		ready("ready " + strconv.Itoa(os.Getpid()))
		ctx := e.ctx
		if *parent > 0 {
			var cancel context.CancelFunc
			ctx, cancel = watchParent(ctx, *parent, lg)
			defer cancel()
		}
		reason, err := r.Run(ctx)
		if err != nil {
			lg.Printf("stopped: %v", err)
			return err
		}
		lg.Printf("stopped: %s", reason)
		return nil
	}
}

func writePID(path string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func removePID(path string) {
	if readPID(path) == os.Getpid() {
		_ = os.Remove(path)
	}
}

// watchParent returns a context that ends when process pid ends. If pid is the parent of this
// process, the parent's death shows as a new parent PID; otherwise the process is probed.
func watchParent(ctx context.Context, pid int, lg *log.Logger) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	isParent := os.Getppid() == pid
	go func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			gone := false
			if isParent {
				gone = os.Getppid() != pid
			} else {
				gone = errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
			}
			if gone {
				lg.Printf("parent process %d ended", pid)
				cancel()
				return
			}
		}
	}()
	return ctx, cancel
}
