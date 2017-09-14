package docker

import (
	"fmt"
	
	// ScaledMarkets packages:
	"utilities/rest"
)

/*******************************************************************************
 * A build step, in a build output (see the DockerBuildOutput type).
 */
type DockerBuildStep struct {
	StepNumber int
	Command string
	UsedCache bool
	ProducedDockerImageId string
}

func NewDockerBuildStep(number int, cmd string) *DockerBuildStep {
	return &DockerBuildStep{
		StepNumber: number,
		Command: cmd,
	}
}

func (step *DockerBuildStep) SetUsedCache() {
	step.UsedCache = true
}

func (step *DockerBuildStep) SetProducedImageId(id string) {
	step.ProducedDockerImageId = id
}

func (step *DockerBuildStep) String() string {
	var s = fmt.Sprintf("Step %d : %s\n", step.StepNumber, step.Command)
	if step.UsedCache { s = s + " ---> Using cache" }
	if step.ProducedDockerImageId != "" { s = s + " ---> " + step.ProducedDockerImageId }
	s = s + "\n"
	return s
}

func (step *DockerBuildStep) AsJSON() string {
	
	var usedCache string
	if step.UsedCache { usedCache = "true" } else { usedCache = "false" }
	return fmt.Sprintf("{\"StepNumber\": %d, \"Command\": \"%s\", \"UsedCache\": %s, " +
		"\"ProducedDockerImageId\": \"%s\"}", step.StepNumber,
		rest.EncodeStringForJSON(step.Command), usedCache, step.ProducedDockerImageId)
}

