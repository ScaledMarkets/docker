package docker

import (
	"fmt"
)

/*******************************************************************************
 * A structured representation of the output of a docker build. Produced by parsing
 * the output from the docker build command.
 */
type DockerBuildOutput struct {
	ErrorMessage string
	FinalDockerImageId string
	Steps []*DockerBuildStep
}

func NewDockerBuildOutput() *DockerBuildOutput {
	return &DockerBuildOutput{
		ErrorMessage: "",
		FinalDockerImageId: "",
		Steps: make([]*DockerBuildStep, 0),
	}
}

func (buildOutput *DockerBuildOutput) AddStep(number int, cmd string) *DockerBuildStep {

	var step = NewDockerBuildStep(number, cmd)
	buildOutput.Steps = append(buildOutput.Steps, step)
	return step
}

func (buildOutput *DockerBuildOutput) SetFinalImageId(id string) {
	buildOutput.FinalDockerImageId = id
}

func (buildOutput *DockerBuildOutput) GetFinalDockerImageId() string {
	return buildOutput.FinalDockerImageId
}

func (buildOutput *DockerBuildOutput) String() string {
	
	var s = ""
	for _, step := range buildOutput.Steps {
		s = s + step.String()
	}
	return s
}

func (buildOutput *DockerBuildOutput) AsJSON() string {
	
	var s = fmt.Sprintf("{\"ErrorMessage\": \"%s\", \"FinalDockerImageId\": \"%s\", \"Steps\": [",
		buildOutput.ErrorMessage, buildOutput.FinalDockerImageId)
	
	for i, step := range buildOutput.Steps {
		if i > 0 { s = s + ", " }
		s = s + step.AsJSON()
	}
	
	s = s + "]}"
	return s
}

