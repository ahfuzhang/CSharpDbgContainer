package debugadmin

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type TargetProcess struct {
	pid    int
	cmd    *exec.Cmd
	broker *LogBroker
	done   chan error
}

func StartTarget(startup string, broker *LogBroker) (*TargetProcess, error) {
	cmd, err := BuildStartupCommand(startup)
	if err != nil {
		return nil, err
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	target := &TargetProcess{
		pid:    cmd.Process.Pid,
		cmd:    cmd,
		broker: broker,
		done:   make(chan error, 1),
	}
	go target.consumeOutput(stdoutPipe)
	go target.consumeOutput(stderrPipe)
	go target.waitForExit()
	return target, nil
}

func (p *TargetProcess) PID() int {
	return p.pid
}

func (p *TargetProcess) Done() <-chan error {
	return p.done
}

func (p *TargetProcess) consumeOutput(reader io.ReadCloser) {
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text() + "\n"
		_, _ = os.Stdout.WriteString(line)
		p.broker.Broadcast(line)
	}
	if err := scanner.Err(); err != nil {
		message := fmt.Sprintf("[log stream error] %v\n", err)
		_, _ = os.Stdout.WriteString(message)
		p.broker.Broadcast(message)
	}
}

func (p *TargetProcess) waitForExit() {
	err := p.cmd.Wait()
	message := fmt.Sprintf("[target exited] pid=%d err=%v\n", p.pid, err)
	_, _ = os.Stdout.WriteString(message)
	p.broker.Broadcast(message)
	p.done <- err
	close(p.done)
}
