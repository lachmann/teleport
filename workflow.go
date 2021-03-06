package main

import (
	log "github.com/sirupsen/logrus"
	"go.starlark.net/starlark"
)

// Workflow is responsible for managing sequential execution steps of a process
type Workflow struct {
	Steps      []step
	Success    func()
	RowCounter int64
	Thread     *starlark.Thread
}

// WorkflowError is the custom error type for instructing workflow how to handle errors
type WorkflowError struct {
	exitCode ExitCode
	err      error
}

func (e *WorkflowError) Error() string {
	return e.err.Error()
}

// ExitCode is an enum for possibe exit codes
type ExitCode int

const (
	Fail ExitCode = iota + 3
	Retry
)

func WorkflowFail(err error) *WorkflowError {
	return &WorkflowError{Fail, err}
}

func WorkflowRetry(err error) *WorkflowError {
	return &WorkflowError{Retry, err}
}

// step represents a single unit of work in the Workflow. Each step function returns nil on success or an error on failure
type step = func() error

var currentWorkflow *Workflow

func (w *Workflow) run() (err error) {
	for _, step := range w.Steps {
		err = step()
		if err != nil {
			w.handleError(err)
			break // Reach here when logrus ExitFunc has been overriden
		}
	}

	return
}

func (w *Workflow) handleError(err error) {
	switch err.(type) {
	case *WorkflowError:
		if err.(*WorkflowError).exitCode == Fail {
			log.Fatal(err)
		} else {
			log.Error(err)
		}
		log.StandardLogger().Exit(int(err.(*WorkflowError).exitCode))
	default:
		log.Fatal(err)
		log.StandardLogger().Exit(int(Fail))
	}
}

// GetRowCounter returns the value of RowCounter for the current workflow
func GetRowCounter() int64 {
	return currentWorkflow.RowCounter
}

// IncrementRowCounter increments the RowCounter for the current workflow
func IncrementRowCounter() {
	currentWorkflow.RowCounter++
}

// GetThread returns the Starlark Thread for the current workflow
func GetThread() *starlark.Thread {
	return currentWorkflow.Thread
}

// RunWorkflow execute a workflow with the provided steps
func RunWorkflow(steps []step, success func()) {
	currentWorkflow = &Workflow{steps, success, 0, &starlark.Thread{}}

	err := currentWorkflow.run()
	if err != nil {
		return
	}

	if currentWorkflow.RowCounter == 0 {
		log.Warn("0 rows processed")
	}

	success()
}
