package autotee

import (
	"io"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/juju/errors"
)

type Cmd struct {
	cmd *exec.Cmd

	waitBegin  chan struct{}
	waitDone   chan struct{}
	waitResult chan error
}

func Command(name string, args ...string) *Cmd {

	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{

		// Make session leader => we can kill it's children too.
		//
		// Setpgid causes the process to be assigned a new process
		// group ID. If it spawns children too, they'll be in the same
		// process group. This allows us to kill the process and all
		// of its subprocesses at once by sending SIGKILL to the
		// negative of its process ID.
		//
		Setpgid: true,

		// Set pdeathsig => kernel kills process when its parent dies.
		//
		// - pdeathsig has to be set /after/ forking, because forking
		//   clears the bit. However Go handles that for us.
		//
		//   Calling exec does not clear pdeathsig (unless set-UID or
		//   set-GID are used). If it did, it would be useless for us.
		//
		// - "Parent" means the OS thread (!) that created the process.
		//   So we need to ensure that its the OS thread that forks that
		//   waits on the process. runtime.LockOSThread helps here.
		//
		//   Note: the thread will not count against GOMAXPROCS
		//   because that only limits the number of goroutines that may
		//   actually run simultaneously.
		//
		Pdeathsig: syscall.SIGKILL,
	}

	return &Cmd{c, make(chan struct{}), make(chan struct{}), make(chan error, 1)}
}

func (c *Cmd) Start() error {
	startResult := make(chan error)

	go func() {
		runtime.LockOSThread()

		if err := c.cmd.Start(); err != nil {
			startResult <- errors.Trace(err)
			close(startResult)
			return
		} else {
			close(startResult)
		}

		<-c.waitBegin

		close(c.waitDone)
		c.waitResult <- errors.Trace(c.cmd.Wait())
		close(c.waitResult)
	}()

	return <-startResult
}

func (c *Cmd) WaitChannel() <-chan error {
	close(c.waitBegin)
	return c.waitResult
}

func (c *Cmd) EndWith(otherCmd *Cmd) {
	go func() {
		<-otherCmd.waitDone

		c.End()
	}()
}

func (c *Cmd) StdinPipe() (io.WriteCloser, error) {
	return c.cmd.StdinPipe()
}

func (c *Cmd) StdoutPipe() (io.ReadCloser, error) {
	return c.cmd.StdoutPipe()
}

func (c *Cmd) SetStdin(r io.Reader) {
	c.cmd.Stdin = r
}

func (c *Cmd) SetStderr(w io.Writer) {
	c.cmd.Stderr = w
}

func (c *Cmd) KillGroup() error {
	pgid, err := syscall.Getpgid(c.cmd.Process.Pid)
	if err != nil {
		return errors.Trace(err)
	}

	// Ignore errors; process may already be dead
	_ = syscall.Kill(-pgid, syscall.SIGKILL)

	return nil
}

// Graceful termination. Blocking!
func (c *Cmd) End() error {

	// Ignore errors; process may already be dead
	_ = syscall.Kill(c.cmd.Process.Pid, syscall.SIGTERM)
	time.Sleep(250 * time.Millisecond)
	_ = syscall.Kill(c.cmd.Process.Pid, syscall.SIGKILL)
	return <-c.WaitChannel()
}
