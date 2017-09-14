package docker

type DockerEngine interface {
	Ping() error
	GetImages() ([]map[string]interface{}, error)
	GetImageInfo(imageName string) (map[string]interface{}, error)
	GetImage(repoNameAndTag, filepath string) error
	BuildImage(buildDirPath, imageFullName string, dockerfileName string,
		paramNames, paramValues []string) (string, error)
	TagImage(imageName, hostAndRepoName, tag string) error
	PushImage(repoFullName, tag, regUserId, regPass, regEmail string) error
	DeleteImage(repoName, tag string) error
}
