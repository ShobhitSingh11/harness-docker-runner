// Copyright 2022 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Polyform License
// that can be found in the LICENSE file.

package handler

import (
	"encoding/json"
	"fmt"
	"github.com/harness/lite-engine/executor"
	"io"
	"net/http"
	"runtime"
	"time"

	"github.com/harness/lite-engine/api"
	"github.com/harness/lite-engine/engine"
	"github.com/harness/lite-engine/engine/spec"
	"github.com/harness/lite-engine/logger"
	"github.com/harness/lite-engine/pipeline"
	pruntime "github.com/harness/lite-engine/pipeline/runtime"
)

// HandleExecuteStep returns an http.HandlerFunc that executes a step
func HandleStartStep(e *pruntime.StepExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := time.Now()

		var s api.StartStepRequest
		err := json.NewDecoder(r.Body).Decode(&s)
		if err != nil {
			WriteBadRequest(w, err)
			return
		}

		if s.MountDockerSocket == nil || *s.MountDockerSocket { // required to support m1 where docker isn't installed.
			s.Volumes = append(s.Volumes, getDockerSockVolumeMount())
		}
		s.Volumes = append(s.Volumes, getSharedVolumeMount())
		if len(s.StartStepRequestConfig.OutputVars) > 0 {
			s.Files = []*spec.File{
				{
					Path:  fmt.Sprintf("/tmp/engine/%s.out", s.ID),
					IsDir: false,
					Mode:  0777,
				},
			}
		}
		ex := executor.GetExecutor()
		stageData, err := ex.Get(s.StageRuntimeID)
		if err != nil {
			logger.FromRequest(r).Errorln(err.Error())
			WriteError(w, err)
		}

		e = stageData.StepExecutor
		s.StartStepRequestConfig.Network = stageData.State.GetNetwork()
		fmt.Println("Step retrieved stage info for %s", s.StageRuntimeID)

		ctx := r.Context()
		if err := e.StartStep(ctx, &s); err != nil {
			WriteError(w, err)
		}

		pollResp, err := e.PollStep(ctx, &api.PollStepRequest{ID: s.ID})
		if err != nil {
			WriteError(w, err)
			return
		}

		pollRespErr := pollResp.Error
		if pollRespErr != "" {
			fmt.Println("Response error:%s", pollResp.Error)
			//response = api.StartStepResponse{CommandExecutionStatus: api.Failure, ErrorMessage: pollRespErr}
			WriteJSON(w, pollResp, http.StatusBadRequest)
		} else {
			//response = api.StartStepResponse{CommandExecutionStatus: api.Success, OutputVars: pollResp.Outputs}
			WriteJSON(w, pollResp, http.StatusOK)
		}

		logger.FromRequest(r).
			WithField("latency", time.Since(st)).
			WithField("time", time.Now().Format(time.RFC3339)).
			Infoln("api: successfully started the step")
	}
}

func HandlePollStep(e *pruntime.StepExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := time.Now()

		var s api.PollStepRequest
		err := json.NewDecoder(r.Body).Decode(&s)
		if err != nil {
			WriteBadRequest(w, err)
			return
		}

		if response, err := e.PollStep(r.Context(), &s); err != nil {
			WriteError(w, err)
		} else {
			WriteJSON(w, response, http.StatusOK)
		}

		logger.FromRequest(r).
			WithField("latency", time.Since(st)).
			WithField("time", time.Now().Format(time.RFC3339)).
			Infoln("api: successfully polled the step response")
	}
}

func HandleStreamOutput(e *pruntime.StepExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := time.Now()

		var s api.StreamOutputRequest
		err := json.NewDecoder(r.Body).Decode(&s)
		if err != nil {
			WriteBadRequest(w, err)
			return
		}

		var (
			count  int
			output io.Writer
		)

		oldData, newData, err := e.StreamOutput(r.Context(), &s)
		if err != nil {
			WriteError(w, err)
			return
		}

		flusher, _ := w.(http.Flusher)
		output = w

		_, _ = output.Write(oldData)
		count += len(oldData)
		if flusher != nil {
			flusher.Flush()
		}

	out:
		for {
			select {
			case <-r.Context().Done():
				break out
			case data, ok := <-newData:
				if !ok {
					break out
				}
				_, _ = output.Write(data)
				count += len(data)
				if flusher != nil {
					flusher.Flush()
				}
			}
		}

		logger.FromRequest(r).
			WithField("latency", time.Since(st)).
			WithField("time", time.Now().Format(time.RFC3339)).
			WithField("count", count).
			Infoln("api: successfully streamed the step log")
	}
}

func getSharedVolumeMount() *spec.VolumeMount {
	return &spec.VolumeMount{
		Name: pipeline.SharedVolName,
		Path: pipeline.SharedVolPath,
	}
}

func getDockerSockVolumeMount() *spec.VolumeMount {
	path := engine.DockerSockUnixPath
	if runtime.GOOS == "windows" {
		path = engine.DockerSockWinPath
	}
	return &spec.VolumeMount{
		Name: engine.DockerSockVolName,
		Path: path,
	}
}
