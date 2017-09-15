package docker

/* Interface for interacting with a Docker Registry version 2.

	https://github.com/docker/distribution/blob/master/docs/insecure.md
	
	What a docker "name" is:

		(From: https://github.com/docker/distribution/blob/master/docs/spec/api.md)
		
		All endpoints will be prefixed by the API version and the repository name:
		
		/v2/<name>/
		
		For example, an API endpoint that will work with the library/ubuntu repository,
		the URI prefix will be:
		
		/v2/library/ubuntu/
		
		This scheme provides rich access control over various operations and methods
		using the URI prefix and http methods that can be controlled in variety of ways.
		
		Classically, repository names have always been two path components where each
		path component is less than 30 characters. The V2 registry API does not enforce this.
		The rules for a repository name are as follows:
		
			1. A repository name is broken up into path components. A component of a
				repository name must be at least one lowercase, alpha-numeric characters,
				optionally separated by periods, dashes or underscores. More strictly,
				it must match the regular expression [a-z0-9]+(?:[._-][a-z0-9]+)*.
				
			2. If a repository name has two or more path components, they must be
				separated by a forward slash ("/").
				
			3. The total length of a repository name, including slashes, must be
				less the 256 characters.
		
		These name requirements only apply to the registry API and should accept
		a superset of what is supported by other docker ecosystem components.
*/



import (
	"fmt"
	"io"
	"os"
	"io/ioutil"
	"net/http"
	"archive/tar"
	"encoding/json"
	"encoding/base64"
	"encoding/hex"
	"crypto/sha256"
	"reflect"
	"strings"
	
	"utilities/utils"
	"utilities/rest"
)

type DockerRegistryImpl struct {
	rest.RestContext
}

var _ DockerRegistry = &DockerRegistryImpl{}

/*******************************************************************************
 * 
 */
func OpenDockerRegistryConnection(host string, port int, userId string,
	password string) (DockerRegistry, error) {
	
	fmt.Println(fmt.Sprintf("Opening connection to registry %s:%s@%s:%d",
		userId, password, host, port))
	
	var registry *DockerRegistryImpl = &DockerRegistryImpl{
		RestContext: *rest.CreateTCPRestContext("http", host, port, userId, password, nil, noop),
	}
	
	fmt.Println("Pinging registry...")
	
	var err error = registry.Ping()
	if err != nil {
		return nil, err
	}
	
	fmt.Println("...received response.")
	
	return registry, nil
}

/*******************************************************************************
 * 
 */
func (registry *DockerRegistryImpl) Close() {
}

/*******************************************************************************
 * 
 */
func (registry *DockerRegistryImpl) Ping() error {
	
	var uri = "v2/"
	
	var response *http.Response
	var err error
	response, err = registry.SendBasicGet(uri)
	if err != nil { return err }
	err = utils.GenerateError(response.StatusCode, response.Status + "; in Ping")
	if err != nil { return err }
	return nil
}

/*******************************************************************************
 * If the specified image exists, return true. The repo name is the image path
 * of the image namespace - if any - and registry repository name, separated by a "/".
 */
func (registry *DockerRegistryImpl) ImageExists(repoName string, tag string) (bool, error) {
	
	// https://github.com/docker/distribution/blob/master/docs/spec/api.md
	// https://docs.docker.com/apidocs/v1.4.0/#!/repositories/GetRepository
	var uri = "v2/" + repoName + "/manifests/" + tag
	//v0: GET /api/v0/repositories/{namespace}/{reponame}
	// Make HEAD request to registry.
	var response *http.Response
	var err error
	response, err = registry.SendBasicHead(uri)
	if err != nil { return false, err }
	if response.StatusCode == 404 { return false, nil }
	err = utils.GenerateError(response.StatusCode, response.Status + "; while checking if image exists")
	if err != nil { return false, err }
	return true, nil
}

/*******************************************************************************
 * 
 */
func (registry *DockerRegistryImpl) LayerExistsInRepo(repoName, digest string) (bool, error) {
	
	var uri = fmt.Sprintf("v2/%s/blobs/%s", repoName, digest)
	var response *http.Response
	var err error
	response, err = registry.SendBasicHead(uri)
	if err != nil { return false, err }
	if response.StatusCode == 404 { return false, nil }
	err = utils.GenerateError(response.StatusCode, response.Status + "; while checking if layer exists")
	if err != nil { return false, err }
	return true, nil
}

/*******************************************************************************
 * If the specified image exists, return true. The repo name is the image path
 * of the image namespace - if any - and registry repository name, separated by a "/".
 */
func (registry *DockerRegistryImpl) GetImageInfo(repoName string, tag string) (digest string,
	layerAr []map[string]interface{}, err error) {
	
	// Retrieve manifest.
	var uri = "v2/" + repoName + "/manifests/" + tag
	var resp *http.Response
	resp, err = registry.SendBasicGet(uri)
	if err != nil { return "", nil, err }
	err = utils.GenerateError(resp.StatusCode, resp.Status + "; while getting image info")
	if err != nil { return "", nil, err }
	
	// Parse description of each layer.
	layerAr, err = parseManifest(resp.Body)
	resp.Body.Close()
	if err != nil { return "", nil, err }
	
	// Retrieve image digest header.
	var headers map[string][]string = resp.Header
	digest = headers["Docker-Content-Digest"][0]
	
	return digest, layerAr, nil
}

/*******************************************************************************
 * 
 */
func (registry *DockerRegistryImpl) GetImage(repoName string, tag string, filepath string) error {
	
	// GET /v2/<name>/manifests/<reference>
	// GET /v2/<name>/blobs/<digest>
	
	// Retrieve manifest.
	var uri = "v2/" + repoName + "/manifests/" + tag
	var resp *http.Response
	var err error
	resp, err = registry.SendBasicGet(uri)
	if err != nil { return err }
	err = utils.GenerateError(resp.StatusCode, resp.Status + "; while getting image")
	if err != nil { return err }
	
	// Parse description of each layer.
	var layerAr []map[string]interface{}
	layerAr, err = parseManifest(resp.Body)
	resp.Body.Close()
	if err != nil { return err }
	
	// Retrieve layers, and add each to a tar archive.
	var tarFile *os.File
	tarFile, err = os.Create(filepath)
	if err != nil { return utils.ConstructServerError(fmt.Sprintf(
		"When creating image file '%s': %s", filepath, err.Error()))
	}
	var tarWriter = tar.NewWriter(tarFile)
	var tempDirPath string
	tempDirPath, err = utils.MakeTempDir()
	if err != nil { return utils.ConstructServerError(fmt.Sprintf(
		"When creating temp directory for writing layer files: %s", err.Error()))
	}
	defer os.RemoveAll(tempDirPath)
	for _, layerDesc := range layerAr {
		
		var layerDigest = layerDesc["blobSum"]
		if layerDigest == nil {
			return utils.ConstructServerError("Did not find blobSum field in response for layer")
		}
		var digest string
		var isType bool
		digest, isType = layerDigest.(string)
		if ! isType { return utils.ConstructServerError("blogSum field is not a string - it is a " +
			reflect.TypeOf(layerDigest).String())
		}
		uri = "v2/" + repoName + "/blobs/" + digest
		resp, err = registry.SendBasicGet(uri)
		if err != nil { return err }
		defer resp.Body.Close()
		err = utils.GenerateError(resp.StatusCode, resp.Status + 
			fmt.Sprintf("when requesting uri: '%s'", uri))
		if err != nil { return err }

		// Create temporary file in which to write layer.
		var layerFile *os.File
		layerFile, err = utils.MakeTempFile(tempDirPath, digest)
		if err != nil { return utils.ConstructServerError(fmt.Sprintf(
			"When creating layer file: %s", err.Error()))
		}
		
		var reader io.ReadCloser = resp.Body
		layerFile, err = os.OpenFile(layerFile.Name(), os.O_WRONLY, 0600)
		if err != nil { return utils.ConstructServerError(fmt.Sprintf(
			"When opening layer file '%s': %s", layerFile.Name(), err.Error()))
		}
		_, err = io.Copy(layerFile, reader)
		if err != nil { return utils.ConstructServerError(fmt.Sprintf(
			"When writing layer file '%s': %s", layerFile.Name(), err.Error()))
		}
		var fileInfo os.FileInfo
		fileInfo, err = layerFile.Stat()
		if err != nil { return utils.ConstructServerError(fmt.Sprintf(
			"When getting status of layer file '%s': %s", layerFile.Name(), err.Error()))
		}
		if fileInfo.Size() == 0 { return utils.ConstructServerError(fmt.Sprintf(
			"Layer file that was written, '%s', has zero size", layerFile.Name()))
		}
		
		// Add file to tar archive.
		var tarHeader = &tar.Header{
			Name: fileInfo.Name(),
			Mode: 0600,
			Size: int64(fileInfo.Size()),
		}
		err = tarWriter.WriteHeader(tarHeader)
		if err != nil {	return utils.ConstructServerError(fmt.Sprintf(
			"While writing layer header to tar archive: , %s", err.Error()))
		}
		
		layerFile, err = os.Open(layerFile.Name())
		if err != nil {	return utils.ConstructServerError(fmt.Sprintf(
			"While opening layer file '%s': , %s", layerFile.Name(), err.Error()))
		}
		_, err := io.Copy(tarWriter, layerFile)
		if err != nil {	return utils.ConstructServerError(fmt.Sprintf(
			"While writing layer content to tar archive: , %s", err.Error()))
		}
	}
	
	err = tarWriter.Close()
	if err != nil {	return utils.ConstructServerError(fmt.Sprintf(
		"While closing tar archive: , %s", err.Error()))
	}
	
	return nil
}

/*******************************************************************************
 * 
 */
func (registry *DockerRegistryImpl) DeleteImage(repoName, tag string) error {
	
	//v2: DELETE /v2/<name>/blobs/<digest>
	//	DELETE /v2/<name>/manifests/<reference>
	//v1: DELETE /api/v0/repositories/{namespace}/{reponame}
	
	// Retrieve manifest.
	var uri = "v2/" + repoName + "/manifests/" + tag
	var resp *http.Response
	var err error
	resp, err = registry.SendBasicGet(uri)
	if err != nil { return err }
	resp.Body.Close()
	err = utils.GenerateError(resp.StatusCode, resp.Status + "; while deleting image")
	if err != nil { return err }
	
	// Parse description of each layer.
	var layerAr []map[string]interface{}
	layerAr, err = parseManifest(resp.Body)
	if err != nil { return err }
	
	// Delete each layer.
	for _, layerDesc := range layerAr {
		
		var layerDigest = layerDesc["blobSum"]
		if layerDigest == nil {
			return utils.ConstructServerError("Did not find blobSum field in response for layer")
		}
		var digest string
		var isType bool
		digest, isType = layerDigest.(string)
		if ! isType { return utils.ConstructServerError("blogSum field is not a string - it is a " +
			reflect.TypeOf(layerDigest).String())
		}
		
		uri = fmt.Sprintf("v2/%s/blobs/%s", repoName, digest)
		var response *http.Response
		var err error
		response, err = registry.SendBasicDelete(uri)
		if err != nil { return err }
		err = utils.GenerateError(response.StatusCode, response.Status + "; while deleting layer")
		if err != nil { return err }
	}
	
	// Delete manifest.
	uri = "v2/" + repoName + "/manifests/" + tag
	resp, err = registry.SendBasicDelete(uri)
	if err != nil { return err }
	
	return nil
}

/*******************************************************************************
 * Registry 2 image push protocol:
 *	1. Upload each layer. (See PushLayer.)
 * 	2. Upload image manifest.
 */
func (registry *DockerRegistryImpl) PushImage(repoName, tag, imageFilePath string) error {
	
	// Create a scratch directory.
	var tempDirPath string
	var err error
	tempDirPath, err = utils.MakeTempDir()
	if err != nil { return err }
	//defer os.RemoveAll(tempDirPath)
	
	// Expand tar file.
	var tarFile *os.File
	tarFile, err = os.Open(imageFilePath)
	if err != nil { return err }
	var tarReader *tar.Reader = tar.NewReader(tarFile)
	
	for { // each tar file entry
		var header *tar.Header
		header, err = tarReader.Next()
		if err == io.EOF { break }
		if err != nil { return err }
		
		if strings.HasSuffix(header.Name, "/") {  // a directory
			
			var dirname = tempDirPath + "/" + header.Name
			err = os.Mkdir(dirname, 0770)
			if err != nil { return err }
			
		} else if (header.Name == "repositories") ||
				strings.HasSuffix(header.Name, "/layer.tar") {
			
			// Write entry to a file.
			var nWritten int64
			var outfile *os.File
			var filename = tempDirPath + "/" + header.Name
			outfile, err = os.OpenFile(filename, os.O_CREATE | os.O_RDWR, 0770)
			if err != nil { return err }
			nWritten, err = io.Copy(outfile, tarReader)
			if err != nil { return err }
			if nWritten == 0 { return utils.ConstructServerError(
				"No data written to " + filename)
			}
			outfile.Close()
		}
	}
	
	// Parse the 'repositories' file. We are expecting a format as,
	//	{"<repo-name>":{"<tag>":"<digest>"}}
	// E.g.,
	//	{"realm4/repo1":{"myimage2":"d2cf21381ce5a17243ec11062b5..."}}
	var repositoriesFile *os.File
	repositoriesFile, err = os.Open(tempDirPath + "/" + "repositories")
	if err != nil { return err }
	var bytes []byte
	bytes, err = ioutil.ReadAll(repositoriesFile)
	if err != nil { return err }
	var obj interface{}
	err = json.Unmarshal(bytes, &obj)
	if err != nil { return err }
	var repositoriesMap map[string]interface{}
	var isType bool
	repositoriesMap, isType = obj.(map[string]interface{})
	if ! isType { return utils.ConstructServerError(
		"repositories file json does not translate to a map[string]interface")
	}
	if len(repositoriesMap) == 0 { return utils.ConstructServerError(
		"No entries found in repository map for image")
	}
	if len(repositoriesMap) > 1 { return utils.ConstructServerError(
		"More than one entry found in repository map for image")
	}
	
	//var oldRepoName string
	//var oldTag string
	var imageDigest string
	for _, tagObj := range repositoriesMap {
		//oldRepoName = rName
		var tagMap map[string]interface{}
		tagMap, isType = tagObj.(map[string]interface{})
		if ! isType { return utils.ConstructServerError(
			"repository json does not translate to a map[string]interface")
		}
		if len(tagMap) == 0 { return utils.ConstructServerError(
			"No entries found in tag map for repo")
		}
		if len(tagMap) > 1 { return utils.ConstructServerError(
			"More than one entry found in tag map for repo")
		}
		for _, tagDigestObj := range tagMap {
			//oldTag = t
			var tagDigest string
			tagDigest, isType = tagDigestObj.(string)
			if ! isType { return utils.ConstructServerError(
				"Digest is not a string")
			}
			imageDigest = tagDigest
		}
	}
	
	// Obtain digest strings and layer paths.
	var scratchDir *os.File
	scratchDir, err = os.Open(tempDirPath)
	if err != nil { return err }
	var layerFilenames []string
	layerFilenames, err = scratchDir.Readdirnames(0)
	if err != nil { return err }
	
	// Send each layer to the registry.
	var layerDigests = make([]string, 0)
	for _, layerFilename := range layerFilenames {  // layer files are named by their digest

		if layerFilename == "repositories" { continue } // not a layer
		
		var layerFilePath = tempDirPath + "/" + layerFilename + "/layer.tar"
		var layerDigest string
		layerDigest, err = registry.PushLayer(layerFilePath, repoName)
		//err = registry.PushLayer(layerFilePath, repoName, layerDigest)
		if err != nil { return err }
		layerDigests = append(layerDigests, layerDigest)
	}
	
	// Send a manifest to the registry.
	err = registry.PushManifest(repoName, tag, imageDigest, layerDigests)
	if err != nil { return err }
	
	os.RemoveAll(tempDirPath)

	return nil
}

/*******************************************************************************
 * Push a layer, using the "chunked" upload registry protocol.
 * Registry 2 layer push protocol:
 *	1. Obtain Location URL:
 		HTTP Method: POST
 		URI: /v2/<name>/blobs/uploads/
 		Response includes a Location header. We call this value 'location'.
 *	2. Send layer:
		HTTP Method: PATCH
		URL: <location from #1>
		Headers:
			Content-Length: <size of chunk>
			Content-Range: 0-<file size -1>
			Content-Type: application/octet-stream
			Authorization: Basic <base 64 encoded userid:password, per RFC 2617>
		Body: <layer binary data>
 *	3. Signal completion of layer upload:
		HTTP Method: PUT
		URL: <location from #1>?digest=<layer digest>
		Headers: ....
 */
func (registry *DockerRegistryImpl) PushLayer(layerFilePath, repoName string) (string, error) {

	// Compute layer signature.
	var digest []byte
	var err error
	digest, err = utils.ComputeFileDigest(sha256.New(), layerFilePath)
	if err != nil { return "", err }
	var digestString = hex.EncodeToString(digest)
	fmt.Println("Computed digest: " + digestString)
	
	// Check if layer already exists in repo.
	var exists bool
	exists, err = registry.LayerExistsInRepo(repoName, digestString)
	if err != nil { return digestString, err }
	if exists { return digestString, nil }
	
	// Get Location header.
	var response *http.Response
	var uri = fmt.Sprintf("v2/%s/blobs/uploads/", repoName)
	response, err = registry.SendBasicFormPost(uri, []string{}, []string{})
	if err != nil { return digestString, err }
	err = utils.GenerateError(response.StatusCode, response.Status + "; while starting layer upload")
	if err != nil { return digestString, err }
	var locations []string = response.Header["Location"]
	if locations == nil { return digestString, utils.ConstructServerError("No Location header") }
	if len(locations) != 1 { return digestString, utils.ConstructServerError("Unexpected Location header") }
	var location string = locations[0]
	//var uuid string = response.Header.Get("Docker-Upload-UUID")
	
	// See docker/distribution/push_v2.go, Upload method.
	// ********See docker/distribution/registry/client/blog_writer.go.
	// See distribution/registry/client/repository.go, Create method.
	//u, err := bs.ub.BuildBlobUploadURL(bs.name, values...)
	//....location, err := sanitizeLocation(resp.Header.Get("Location"), u)
	//req.URL.RawQuery = values.Encode()
	
	var layerFile *os.File
	layerFile, err = os.Open(layerFilePath)
	if err != nil { return digestString, err }
	var fileInfo os.FileInfo
	fileInfo, err = layerFile.Stat()
	if err != nil { return digestString, err }
	
	//location = strings.TrimPrefix(location, "/")
	
	// Send the request using the URL provided.
	var url = location
	
	// Construct Authorization header.
	// Ref: https://tools.ietf.org/html/rfc2617 section 2.
	var encoded string = base64.StdEncoding.EncodeToString(
		[]byte(fmt.Sprintf("%s:%s", registry.GetUserId(), registry.GetPassword())))
	var authHeaderValue = "Basic " + encoded
	
	// Assemble headers.
	var fileSize int64 = fileInfo.Size()
	var headers = map[string]string{
		"Content-Length": fmt.Sprintf("%d", fileSize),
		"Content-Range": fmt.Sprintf("0-%d", (fileSize-1)),
		"Content-Type": "application/octet-stream",
		"Authorization": authHeaderValue,
	}
	
	// Construct request.
	var request *http.Request
	request, err = http.NewRequest("PATCH", url, layerFile)
	if err != nil { return digestString, err }
	
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	
	/*
	// Submit the request (sends the layer).
	fmt.Println("PushLayer: url='" + url + "'")
	response, err = registry.GetHttpClient().Do(request)
	fmt.Println("PushLayer: response Status='" + response.Status + "'")
	
	locations = response.Header["Location"]
	location = ""
	if len(locations) > 0 { location = locations[0] }
	//response, err = registry.SendBasicStreamPut(uri, headers, layerFile)
	//if err != nil { return err }
	
	err = utils.GenerateError(response.StatusCode, response.Status + "; while posting layer")
	
	if err != nil {
		var bytes []byte
		var err2 error
		bytes, err2 = ioutil.ReadAll(response.Body)
		if err2 != nil { fmt.Println(err2.Error()); return err }
		fmt.Println(string(bytes))
	}

	if err != nil { return err }
	
	*/
	
	// Signal completion of upload.
	// .... not clear how to construct the URL.
//	var parts []string = strings.SplitAfter(location, "?")
//	if len(parts) != 2 { return utils.ConstructServerError("Malformed location: " + location) }
//	url = parts[0] + "digest=" + digestString

	url = location + "&digest=sha256:" + digestString
	//uri = fmt.Sprintf("/v2/%s/blob/uploads/%s?digest=%s", repoName, uuid, digestString)
	
	request, err = http.NewRequest("PUT", url, layerFile)
	if err != nil { return digestString, err }

	headers = map[string]string{
		"Content-Length": fmt.Sprintf("%d", fileSize),
		"Content-Range": fmt.Sprintf("0-%d", (fileSize-1)),
		"Content-Type": "application/octet-stream",
		"Authorization": authHeaderValue,
		//"Content-Length": "0",
		//"Content-Range": fmt.Sprintf("%d-%d", (fileSize), (fileSize-1)),
		//"Content-Type": "application/octet-stream",
		//"Authorization": authHeaderValue,
	}
	
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	
	response, err = registry.GetHttpClient().Do(request)
	if err != nil { return digestString, err }
	err = utils.GenerateError(response.StatusCode, response.Status)

	if err != nil {
		var bytes []byte
		var err2 error
		bytes, err2 = ioutil.ReadAll(response.Body)
		if err2 != nil { fmt.Println(err2.Error()); return digestString, err }
		fmt.Println(string(bytes))
	}
		
	if err != nil { return digestString, err }
	
	return digestString, nil
}

/*
func sanitizeLocation(location, base string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	locationURL, err := url.Parse(location)
	if err != nil {
		return "", err
	}

	return baseURL.ResolveReference(locationURL).String(), nil
}
*/

/*******************************************************************************
 * 
 */
func (registry *DockerRegistryImpl) PushManifest(repoName, tag, imageDigestString string,
	layerDigestStrings []string) error {
	
	var uri = fmt.Sprintf("v2/%s/manifests/%s", repoName, tag)
	//var uri = fmt.Sprintf("v2/%s/manifests/sha256:%s", repoName, imageDigestString)
	//var uri = fmt.Sprintf("v2/%s/manifests/sha256:%s", repoName + ":" + tag, imageDigestString)
	
	var url = registry.GetScheme() + "://" + registry.GetHostname()
	if registry.GetPort() != 0 { url = url + fmt.Sprintf(":%d", registry.GetPort()) }
	url = url + "/" + uri
	
	fmt.Println("url=" + url)
	
	var manifest = fmt.Sprintf("{" +
		"\"name\": \"%s\", \"tag\": \"%s\", \"fsLayers\": [", repoName, tag)
	
	// Info on JSON Web Tokens:
	// https://jwt.io/introduction/
	// https://tools.ietf.org/html/rfc7515
	// Issue posted to github docker/distribution project:
	// https://github.com/docker/distribution/pull/1702#issuecomment-219178800
	
	
	for i, layerDigestString := range layerDigestStrings {
		if i > 0 { manifest = manifest + ",\n" }
		manifest = manifest + fmt.Sprintf("{\"blobSum\": \"sha256:%s\"}", layerDigestString)
	}
	
	manifest = manifest + "]}"
	
	fmt.Println("manifest:")
	fmt.Println(manifest)
	fmt.Println()
	
	var stringReader *strings.Reader = strings.NewReader(manifest)
	
	var encoded string = base64.StdEncoding.EncodeToString(
		[]byte(fmt.Sprintf("%s:%s", registry.GetUserId(), registry.GetPassword())))
	var authHeaderValue = "Basic " + encoded

	var headers = map[string]string{
		"Content-Length": fmt.Sprintf("%d", len(manifest)),
		"Content-Type": "application/json; charset=utf-8",
		"Authorization": authHeaderValue,
	}
	
	var request *http.Request
	var err error
	request, err = http.NewRequest("PUT", url, stringReader)
	if err != nil { return err }
	
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	
	var response *http.Response
	response, err = registry.GetHttpClient().Do(request)
	if err != nil { return err }
	
	//response, err = registry.SendBasicStreamPut(uri, headers, stringReader)
	if err != nil { return err }
	err = utils.GenerateError(response.StatusCode, response.Status + "; while putting manifest")
	if err != nil {
		var bytes []byte
		var err2 error
		bytes, err2 = ioutil.ReadAll(response.Body)
		if err2 != nil { fmt.Println("While readoing response body, " + err2.Error()); } else {
			fmt.Println("Response body:")
			fmt.Println(string(bytes))
			fmt.Println("\nEnd of Response body.")
		}
	}
	if err != nil { return err }
	
	return nil
}

/*******************************************************************************
 * Return an array of maps, one for each layer, and each containing the attributes
 * of the layer.
 */
func parseManifest(body io.ReadCloser) ([]map[string]interface{}, error) {
	
	var responseMap map[string]interface{}
	var err error
	responseMap, err = rest.ParseResponseBodyToMap(body)
	if err != nil { return nil, err }
	body.Close()
	var layersObj = responseMap["fsLayers"]
	if layersObj == nil {
		return nil, utils.ConstructServerError("Did not find fsLayers field in body")
	}
	var isType bool
	var layerArObj []interface{}
	layerArObj, isType = layersObj.([]interface{})
	if ! isType { return nil, utils.ConstructServerError(
		"Type of layer description is " + reflect.TypeOf(layersObj).String())
	}
	var layerAr = make([]map[string]interface{}, 0)
	for _, obj := range layerArObj {
		var m map[string]interface{}
		m, isType = obj.(map[string]interface{})
		if ! isType { return nil, utils.ConstructServerError(
			"Type of layer object is " + reflect.TypeOf(obj).String())
		}
		layerAr = append(layerAr, m)
	}
	
	return layerAr, nil
}

/*******************************************************************************
 * 
 */
func noop(req *http.Request, s string) {
}
