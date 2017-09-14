package docker

type DockerRegistry interface {
	Close()
	Ping() error
	ImageExists(repoName, tag string) (bool, error)
	LayerExistsInRepo(repoName, digest string) (bool, error)
	GetImageInfo(repoName, tag string) (digest string, 
		layerAr []map[string]interface{}, err error)
	GetImage(repoName, tag, filepath string) error
	DeleteImage(repoName, tag string) error
	PushImage(repoName, tag, imageFilePath string) error
	PushLayer(layerFilePath, repoName string) (string, error)
	PushManifest(repoName, tag, imageDigestString string, layerDigestStrings []string) error
}
