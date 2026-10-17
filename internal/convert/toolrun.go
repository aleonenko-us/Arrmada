package convert

import (
	"context"
	"time"

	"github.com/tristenlammi/arrmada/internal/toolrun"
)

// Every tool a conversion runs — the encode, test clips, SSIM and VMAF scoring, crop
// detection, the HDR10+ read and inject, the remuxes and the full-decode check — is started
// through s.tool. That is what makes each of them gentle and pausable, not just the encode:
//
//   - its own process group, killed whole when the job is cancelled or times out;
//   - nice 19, with the idle I/O class for side reads (toolrun.Idle) or the lowest
//     best-effort level for the encode and the final remux (toolrun.Background), so Plex
//     wins the disk and the CPU;
//   - registered with the job (trackProc), so the pause loop freezes it with SIGSTOP while
//     someone is watching or outside the encode hours, and continues it after.
//
// For later work (the resumable pieced encode, CONV-27): start every piece and every check
// with s.tool(ctx, job, class, bin, args...) and its Start/Wait/Run/Output/CombinedOutput,
// put any time limit on a step through s.pausableTimeout so a frozen step doesn't use up
// its budget, and call s.waitAllowed before a heavy step. Steps that don't have the job to
// hand get it from ctx: runJob puts it there (withJob), so passing nil is fine below it.
// ffprobe runs as toolrun.Normal through toolrun.Command directly — it is light, and a
// paused probe would only hold up the job's bookkeeping.

// tool builds a command for one conversion step: started through toolrun at the given
// class, and registered with job (or the job carried by ctx when job is nil) so pausing
// reaches it.
func (s *Service) tool(ctx context.Context, job *Job, class toolrun.Class, bin string, args ...string) *toolrun.Cmd {
	cmd := toolrun.Command(ctx, class, bin, args...)
	if bin == s.ffmpeg {
		cmd.Env = libvaEnv(args) // the VA driver for the GPU this run opens (libva.go)
	}
	if job == nil {
		job = jobFrom(ctx)
	}
	if job != nil {
		cmd.OnStart = func(pgid int) { s.trackProc(job, pgid) }
		cmd.OnExit = func(pgid int) { s.untrackProc(job, pgid) }
	}
	return cmd
}

type jobCtxKey struct{}

// withJob carries the job a conversion step belongs to, so helpers several calls down
// (crop detection, the SSIM windows, the HDR10+ read) register their tools with it without
// every signature taking the job.
func withJob(ctx context.Context, job *Job) context.Context {
	return context.WithValue(ctx, jobCtxKey{}, job)
}

// jobFrom is the job withJob put in ctx, or nil.
func jobFrom(ctx context.Context) *Job {
	j, _ := ctx.Value(jobCtxKey{}).(*Job)
	return j
}

// canSuspend reports whether running tools can be paused on this platform.
const canSuspend = toolrun.CanPause

// suspendGroup freezes one of a job's tools (and anything it spawned). The tool keeps its
// place: resumeGroup continues it exactly where it stopped, which is how a job started
// inside the encode hours waits out the day instead of either running into prime time or
// being thrown away and redone.
func suspendGroup(pgid int) error { return toolrun.Pause(pgid) }

// resumeGroup continues a group frozen by suspendGroup.
func resumeGroup(pgid int) error { return toolrun.Resume(pgid) }

// pausedTick is how often pausableTimeout looks at the job; a var for tests.
var pausedTick = time.Second

// pausableTimeout is context.WithTimeout that only counts time the job is running. A step
// frozen while someone watches a film (or overnight, outside the encode hours) would
// otherwise run out of time while it can't make progress, and a quality check that times
// out keeps the original — so a long Plex evening would cost the whole encode.
func (s *Service) pausableTimeout(ctx context.Context, job *Job, budget time.Duration) (context.Context, context.CancelFunc) {
	if job == nil {
		job = jobFrom(ctx)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		t := time.NewTicker(pausedTick)
		defer t.Stop()
		last, used := time.Now(), time.Duration(0)
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if !s.isPaused(job) {
					used += now.Sub(last)
				}
				last = now
				if used >= budget {
					cancel(context.DeadlineExceeded)
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}

// isPaused reports whether a job is paused right now (its tools frozen).
func (s *Service) isPaused(job *Job) bool {
	if job == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return job.Paused != ""
}
