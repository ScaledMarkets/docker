/*******************************************************************************
 * Provide abstract functions that we need from docker and docker registry.
 * This module relies on implementations of DockerEngine and DockerRegistry.
 *
 * Copyright Scaled Markets, Inc.
 */
package docker

import (
	"fmt"
	"os"
	"io"
	//"io/ioutil"
	"bufio"
	"strings"
	"unicode/utf8"
	"encoding/json"
	//"os/exec"
	//"errors"
	"regexp"
	"reflect"
	
	// SafeHarbor packages:
	"utilities"
	"rest"
)

/* Replace with REST calls.
Registry 2.0:
https://github.com/docker/distribution/tree/master/docs
https://github.com/docker/distribution/tree/master/docs/spec
https://github.com/docker/distribution/blob/master/docs/spec/api.md

SSL config:
https://www.digitalocean.com/community/tutorials/how-to-set-up-a-private-docker-registry-on-ubuntu-14-04

Registry 1.4:
https://docs.docker.com/apidocs/v1.4.0/
	
Engine:
https://github.com/docker/docker/blob/master/docs/reference/api/docker_remote_api_v1.24.md

Image format:
https://github.com/docker/docker/blob/master/image/spec/v1.md
*/

type DockerServices struct {
	Registry DockerRegistry
	Engine DockerEngine
}

/*******************************************************************************
 * 
 */
func NewDockerServices(registry DockerRegistry, engine DockerEngine) *DockerServices {
	return &DockerServices{
		Registry: registry,
		Engine: engine,
	}
}

/*******************************************************************************
 * 
 */
func (dockerSvcs *DockerServices) BuildDockerfile(dockerfileExternalFilePath,
	dockerfileName, dockerImageName, tag string,
	paramNames, paramValues []string) (string, error) {
	
	var exists bool = false
	var err error = nil
	var fullName = dockerImageName
	if dockerSvcs.Registry == nil {  // no registry
		// Check if image exists in engine.
		if tag != "" { fullName = fullName + ":" + tag }
		_, err = dockerSvcs.Engine.GetImageInfo(fullName)
		if err == nil { exists = true }
	} else {
		exists, err = dockerSvcs.Registry.ImageExists(dockerImageName, tag)
		//exists, err = dockerSvcs.Registry.ImageExists(realmName + "/" + repoName, imageName)
	}
	
	if exists {
		return "", utilities.ConstructUserError(
			"Image with name " + dockerImageName + ":" + tag + " already exists.")
	}
	
	// Create a temporary directory to serve as the build context.
	var tempDirPath string
	tempDirPath, err = utilities.MakeTempDir()
	if err != nil { return "", err }
	//....TO DO: Is the above a security problem? Do we need to use a private
	// directory? I think so.
	defer func() {
		fmt.Println("Removing all files at " + tempDirPath)
		os.RemoveAll(tempDirPath)
	}()
	fmt.Println("Temp directory = ", tempDirPath)

	// Copy dockerfile to that directory.
	var in, out *os.File
	in, err = os.Open(dockerfileExternalFilePath)
	if err != nil { return "", err }
	var dockerfileCopyPath string = tempDirPath + "/" + dockerfileName
	out, err = os.Create(dockerfileCopyPath)
	if err != nil { return "", err }
	_, err = io.Copy(out, in)
	if err != nil { return "", err }
	err = out.Close()
	if err != nil { return "", err }
	fmt.Println("Copied Dockerfile to " + dockerfileCopyPath)
	
//	fmt.Println("Changing directory to '" + tempDirPath + "'")
//	err = os.Chdir(tempDirPath)
//	if err != nil { return apitypes.NewFailureDescFromError(err) }
	
	// Create a the docker build command.
	// https://docs.docker.com/reference/commandline/build/
	// REPOSITORY                      TAG                 IMAGE ID            CREATED             VIRTUAL SIZE
	// docker.io/cesanta/docker_auth   latest              3d31749deac5        3 months ago        528 MB
	// Image id format: <hash>[:TAG]
	
	var imageFullName = dockerImageName + ":" + tag
	var outputStr string
	outputStr, err = dockerSvcs.Engine.BuildImage(tempDirPath, imageFullName, 
		dockerfileName, paramNames, paramValues)
	if err != nil { return outputStr, err }
	
	if dockerSvcs.Registry != nil {  // a registry
		// Push new image to registry. Use the engine's push image feature.
		// Have not been able to get the engine push command to work. The docker client
		// end up reporting "Pull session cancelled".
		//err = dockerSvcs.Engine.PushImage(imageRegistryTag)
		
		// Obtain image as a file.
		var tempDirPath2 string
		tempDirPath2, err = utilities.MakeTempDir()
		if err != nil { return outputStr, err }
		defer os.RemoveAll(tempDirPath2)
		var imageFile *os.File
		imageFile, err = utilities.MakeTempFile(tempDirPath2, "")
		if err != nil { return outputStr, err }
		var imageFilePath = imageFile.Name()
		err = dockerSvcs.Engine.GetImage(imageFullName, imageFilePath)
		if err != nil { return outputStr, err }
		
		// Obtain the image digest.
		var info map[string]interface{}
		info, err = dockerSvcs.Engine.GetImageInfo(imageFullName)
		if err != nil { return outputStr, err }
		var digest = info["Id"]
		var digestString string
		var isType bool
		digestString, isType = digest.(string)
		if digest == nil {
			fmt.Println("Digest is nil; map returned from GetImageInfo:")
			rest.PrintMap(info)
			return outputStr, utilities.ConstructServerError("Digest is nil") }
		if ! isType { return outputStr, utilities.ConstructServerError(
			"checksum is not a string: it is a " + reflect.TypeOf(digest).String())
		}
		if digestString == "" { return outputStr, utilities.ConstructServerError(
			"No checksum field found for image")
		}
		
		// Push image to registry - all layers and manifest.
		err = dockerSvcs.Registry.PushImage(dockerImageName, tag, imageFilePath)
		if err != nil { return outputStr, err }
		
		// Tag the uploaded image with its name.
		//err = dockerSvcs.Registry.TagImage(digestString, ....repoName, ....tag)
		if err != nil { return outputStr, err }
	}
	
	return outputStr, err
}

/*******************************************************************************
 * Parse the string that is returned by the docker build command.
 * Partial results are returned, but with an error.
 *
 * Parse algorithm:
	States:
	1. Looking for next step:
		When no more lines, done but incomplete.
		When encounter "Step ",
			Set step no.
			Set command.
			Read next line
			If no more lines,
				Then done but incomplete.
				Else go to state 2.
		When encounter "Successfully built"
			Set final image id
			Done and complete.
		When encounter "Error"
			Done with error
		Otherwise read line (i.e., skip the line) and go to state 1.
	2. Looking for step parts:
		When encounter " ---> ",
			Recognize and (if recognized) add part.
			Read next line.
			if no more lines,
				Then done but incomplete.
				Else go to state 2
		Otherwise go to state 1

 * Sample output:
	Sending build context to Docker daemon  2.56 kB\rSending build context to Docker daemon  2.56 kB\r\r
	Step 0 : FROM ubuntu:14.04
	 ---> ca4d7b1b9a51
	Step 1 : MAINTAINER Steve Alexander <steve@scaledmarkets.com>
	 ---> Using cache
	 ---> 3b6e27505fc5
	Step 2 : ENV REFRESHED_AT 2015-07-13
	 ---> Using cache
	 ---> 5d6cdb654470
	Step 3 : RUN apt-get -yqq update
	 ---> Using cache
	 ---> c403414c8254
	Step 4 : RUN apt-get -yqq install apache2
	 ---> Using cache
	 ---> aa3109896080
	Step 5 : VOLUME /var/www/html
	 ---> Using cache
	 ---> 138c71e28dc1
	Step 6 : WORKDIR /var/www/html
	 ---> Using cache
	 ---> 8aa5cb29ae1d
	Step 7 : ENV APACHE_RUN_USER www-data
	 ---> Using cache
	 ---> 7f721c24718d
	Step 8 : ENV APACHE_RUN_GROUP www-data
	 ---> Using cache
	 ---> 05a094d0d47f
	Step 9 : ENV APACHE_LOG_DIR /var/log/apache2
	 ---> Using cache
	 ---> 30424d879506
	Step 10 : ENV APACHE_PID_FILE /var/run/apache2.pid
	 ---> Using cache
	 ---> d163597446d6
	Step 11 : ENV APACHE_RUN_DIR /var/run/apache2
	 ---> Using cache
	 ---> 065c69b4a35c
	Step 12 : ENV APACHE_LOCK_DIR /var/lock/apache2
	 ---> Using cache
	 ---> 937eb3fd1f42
	Step 13 : RUN mkdir -p $APACHE_RUN_DIR $APACHE_LOCK_DIR $APACHE_LOG_DIR
	 ---> Using cache
	 ---> f0aebcae65d4
	Step 14 : EXPOSE 80
	 ---> Using cache
	 ---> 5f139d64c08f
	Step 15 : ENTRYPOINT /usr/sbin/apache2
	 ---> Using cache
	 ---> 13cf0b9469c1
	Step 16 : CMD -D FOREGROUND
	 ---> Using cache
	 ---> 6a959754ab14
	Successfully built 6a959754ab14
	
 * Another sample:
	Sending build context to Docker daemon 20.99 kB
	Sending build context to Docker daemon 
	Step 0 : FROM docker.io/cesanta/docker_auth:latest
	 ---> 3d31749deac5
	Step 1 : RUN echo moo > oink
	 ---> Using cache
	 ---> 0b8dd7a477bb
	Step 2 : FROM 41477bd9d7f9
	 ---> 41477bd9d7f9
	Step 3 : RUN echo blah > afile
	 ---> Running in 3bac4e50b6f9
	 ---> 03dcea1bc8a6
	Removing intermediate container 3bac4e50b6f9
	Successfully built 03dcea1bc8a6
 */
func ParseBuildCommandOutput(buildOutputStr string) (*DockerBuildOutput, error) {
	
	fmt.Println("ParseBuildCommandOutput: A")  // debug
	fmt.Println("Build output:")  // debug
	fmt.Println(buildOutputStr)  // debug
	fmt.Println("End of build output.")  // debug
	
	var output *DockerBuildOutput = NewDockerBuildOutput()
	
	var lines = strings.Split(buildOutputStr, "\n")
	var state int = 1
	var step *DockerBuildStep
	var lineNo int = 0
	for {
		
		if lineNo >= len(lines) {
			return output, utilities.ConstructServerError("Incomplete")
		}
		
		var line string = lines[lineNo]
		
		switch state {
			
		case 1: // Looking for next step
			
			var therest = strings.TrimPrefix(line, "Step ")
			if len(therest) < len(line) {
				// Syntax is: number space colon space command
				var stepNo int
				var cmd string
				fmt.Sscanf(therest, "%d", &stepNo)
				
				var separator = " : "
				var seppos int = strings.Index(therest, separator)
				if seppos != -1 { // found
					cmd = therest[seppos + len(separator):] // portion from seppos on
					step = output.AddStep(stepNo, cmd)
				}
				
				lineNo++
				state = 2
				continue
			}
			
			therest = strings.TrimPrefix(line, "Successfully built ")
			if len(therest) < len(line) {
				var id = therest
				output.SetFinalImageId(id)
				return output, nil
			}
			
			therest = strings.TrimPrefix(line, "Error")
			if len(therest) < len(line) {
				output.ErrorMessage = therest
				return output, utilities.ConstructServerError(output.ErrorMessage)
			}
			
			lineNo++
			state = 1
			continue
			
		case 2: // Looking for step parts
			
			if step == nil {
				output.ErrorMessage = "Internal error: should not happen"
				return output, utilities.ConstructServerError(output.ErrorMessage)
			}

			var therest = strings.TrimPrefix(line, " ---> ")
			if len(therest) < len(line) {
				if strings.HasPrefix(therest, "Using cache") {
					step.SetUsedCache()
				} else {
					if strings.Contains(" ", therest) {
						// Unrecognized line - skip it but stay in the current state.
					} else {
						step.SetProducedImageId(therest)
					}
				}
				lineNo++
				continue
			}
			
			state = 1
			
		default:
			output.ErrorMessage = "Internal error: Unrecognized state"
			return output, utilities.ConstructServerError(output.ErrorMessage)
		}
	}
	output.ErrorMessage = "Did not find a final image Id"
	return output, utilities.ConstructServerError(output.ErrorMessage)
}

/*******************************************************************************
 * Parse the string that is returned by the docker daemon REST build function.
 * Partial results are returned, but with an error.
 */
func ParseBuildRESTOutput(restResponse string) (*DockerBuildOutput, error) {
	
	fmt.Println("ParseBuildRESTOutput: A")  // debug
	var outputstr string
	var err error
	outputstr, err = extractBuildOutputFromRESTResponse(restResponse)
	fmt.Println("ParseBuildRESTOutput: B")  // debug
	if err != nil { return nil, err }
	fmt.Println("ParseBuildRESTOutput: C")  // debug
	var buildOutput *DockerBuildOutput
	buildOutput, err = ParseBuildCommandOutput(outputstr)
	fmt.Println("ParseBuildRESTOutput: D")  // debug
	return buildOutput, err
}

/*******************************************************************************
 * Parse the specified dockerfile and return any ARGs that it has.
 * Syntax:
 	buildfile			::= line*
 	line				::= instruction argument* | comment
 	comment				::= '#' <all characters through end of line>
 	insruction			::= arg_instruction | otherinstruction
 	arg_instruction		::= [aA][rR][gG] arg_name opt_assignment
 	otherinstruction	::= [a-zA-Z]+
 	arg_name			::= [a-zA-Z]+
 	opt_assignment		::= "=" string_expr | <nothing>
 	string_expr			<all characters through end of line>
 	
 * Parse algorithm:
	For each line:
	1. Looking for next instruction:
		When no more lines, done.
		When encounter [aA][rR][gG] beginning in column 1,
			Go to state 2.
		When encounter anything else,
			Skip line.
	2. Looking for arg_instruction parts:
		Obtain arg_name.
		Obtain opt_assignment, if any.
		If any error, abort.
 */
func ParseDockerfile(dockerfileContent string) ([]*DockerfileExecParameterValueDesc, error) {
	
	var isAlphaChar = func(c rune) bool {
		return ((c >= 'a') && (c <= 'z')) || ((c >= 'A') && (c <= 'Z')) ||
			(c == '_') || (c == '-')
	}
	
	var isNumeric = func(c rune) bool {
		return (c >= '0') && (c <= '9')
	}
	
	/**
	 * A token is any unbroken sequence of [a-zA-Z0-9]+ or a non-whitespace character.
	 * Returns "" if no more tokens.
	 */
	var getToken = func(line string) (token, restOfLine string) {
		
		var trimmedLine = strings.TrimLeft(line, " \t")
		if len(trimmedLine) == 0 { return "", "" }
		
		// Determine if a special character.
		var c rune
		c, _ = utf8.DecodeRuneInString(trimmedLine[0:1])
		if ! isAlphaChar(c) { return trimmedLine[0:1], trimmedLine[1:] }
		
		// Not a special character - get alphanumeric token.
		var pos = 1
		for { // each character pos of trimmedLine, starting from 0,
			if pos == len(trimmedLine) { break }
			if strings.ContainsAny(trimmedLine[pos:pos+1], " \t") { break }
			c, _ = utf8.DecodeRuneInString(trimmedLine[pos:pos+1])
			if ! (isAlphaChar(c) || isNumeric(c)) { break }
			pos++
		}
		
		return trimmedLine[:pos], trimmedLine[pos:]
	}
	
	var lines = strings.Split(dockerfileContent, "\n")
	
	var paramValueDescs = make([]*DockerfileExecParameterValueDesc, 0)
	var lineNo = -1
	for {
		lineNo++
		if lineNo >= len(lines) { break }  // done
		
		var line string = lines[lineNo]
		
		if len(line) == 0 { continue }  // skip blank lines.
		if strings.ContainsAny(line[0:1], " \t") { continue }  // skip continuation lines.
		if strings.HasPrefix(line, "#") { continue }  // skip comment lines.
		var restOfLine string
		var instructionName string
		instructionName, restOfLine = getToken(line)
		if instructionName == "" { continue }  // skip blank line
		if strings.ToUpper(instructionName) == "ARG" {
			// Looking for instruction parts.
			var argName string
			argName, restOfLine = getToken(restOfLine)
			if argName == "" { return nil, utilities.ConstructUserError(
				"No argument name in ARG instruction") }
			// Looking for opt_assignment, if any.
			var equalSign string
			var stringExpr = ""
			equalSign, restOfLine = getToken(restOfLine)
			if equalSign == "=" {
				stringExpr = restOfLine
			}
			var paramValueDesc *DockerfileExecParameterValueDesc
			paramValueDesc = NewDockerfileExecParameterValueDesc(argName, stringExpr) 
			paramValueDescs = append(paramValueDescs, paramValueDesc)
		}
	}
	
	return paramValueDescs, nil
}

/*******************************************************************************
 * Retrieve the specified image from the registry and store it in a file.
 * Return the file path.
 */
func (dockerSvcs *DockerServices) SaveImage(imageName, tag string) (string, error) {

	fmt.Println("Creating temp file to save the image to...")
	var tempFile *os.File
	var err error
	tempFile, err = utilities.MakeTempFile("", "")
	// TO DO: Is the above a security issue?
	if err != nil { return "", err }
	var tempFilePath = tempFile.Name()
	
	if dockerSvcs.Registry == nil {  // no registry
		
		var repoNameAndTag = imageName
		if tag != "" { repoNameAndTag = repoNameAndTag + ":" + tag }
		err = dockerSvcs.Engine.GetImage(repoNameAndTag, tempFilePath)
		if err != nil { return "", err }
		
	} else {
	
		err = dockerSvcs.Registry.GetImage(imageName, tag, tempFilePath)
		if err != nil { return "", err }
	}
	
	return tempFilePath, nil
}

/*******************************************************************************
 * Return the digest of the specified Docker image, as computed by the file''s registry.
 */
func (dockerSvcs *DockerServices) GetDigest(imageId string) ([]byte, error) {
	
	return []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, nil
	/*
	if dockerSvcs.Registry == nil {
		var imageName = ....
		var info map[string]interface{}
		var err error
		info, err = dockerSvcs.Engine.GetImageInfo(imageName)
		var obj interface{} = info["RepoDigests"]
		if obj == nil { return nil, utilities.ConstructServerError("No digest found") }
		var objAr []interface{}
		var isType bool
		objAr, isType = obj.([]interface)
		if ! isType { return nil, utilities.ConstructServerError("RepoDigests field is not an array") }
		for _, obj := range objAr {
			var str string
			str, isType = obj.(string)
			if ! isType { return nil, utilities.ConstructError("Digest value is not a string") }
			var parts []string
			parts = strings.Split(str, "@")
			if len(parts) != 2 { return nil, utilities.ConstructError("Did not find digest in string") }
			var digest = parts[1]
			parts = strings.Split(digest, ":")
			if len(parts) != 2 { return nil, utilities.ConstructError("Digest ill-formed - no ':'") }
			var hashValue = parts[1]
			....
		}
		
	} else {
		....
	}
	*/
}


/*******************************************************************************
 * Return the signature of the specified Docker image, as computed by the file''s registry.
 */
func GetSignature(imageId string) ([]byte, error) {
	return []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, nil
}

/*******************************************************************************
 * 
 */
func (dockerSvcs *DockerServices) RemoveDockerImage(repoName, tag string) error {
	
	// Delete from registry.
	var err error
	err = dockerSvcs.Engine.DeleteImage(repoName, tag)
	if dockerSvcs.Registry != nil {
		err = dockerSvcs.Registry.DeleteImage(repoName, tag)
	}
	if err != nil { return err }
	
	// Delete local engine copy as well, if it exists.
	return nil
}

/*******************************************************************************
 * Check that repository name component matches "[a-z0-9]+(?:[._-][a-z0-9]+)*".
 * I.e., first char is a-z or 0-9, and remaining chars (if any) are those or
 * a period, underscore, or dash. If rules are satisfied, return nil; otherwise,
 * return an error.
 */
func NamePartConformsToDockerRules(part string) error {
	
	matched, err := regexp.MatchString("^[a-z0-9\\-_]*$", part)
	if err != nil { return utilities.ConstructServerError("Unexpected internal error") }
	if ! matched { return utilities.ConstructServerError("Name does not conform to docker rules") }
	return nil
}

/*******************************************************************************
 * 
 */
func ConstructDockerImageName(shRealmName,
	shRepoName, shImageName, version string) (imageName, tag string) {

	return (shRealmName + "/" + shRepoName + "/" + shImageName), version
}

/*******************************************************************************
 * 
 */
type DockerfileExecParameterValueDesc struct {
	rest.ParameterValueDesc
}

func NewDockerfileExecParameterValueDesc(name string, strValue string) *DockerfileExecParameterValueDesc {
	var paramValueDesc = rest.NewParameterValueDesc(name, strValue)
	return &DockerfileExecParameterValueDesc{
		ParameterValueDesc: *paramValueDesc,
	}
}

func (desc *DockerfileExecParameterValueDesc) AsJSON() string {
	return fmt.Sprintf(" {\"Name\": \"%s\", \"Value\": \"%s\"}",
		desc.Name, rest.EncodeStringForJSON(desc.StringValue))
}



/*******************************************************************************
								Internal methods
*******************************************************************************/



/*******************************************************************************
 * The docker daemon build function - a REST function - returns a series of
 * JSON objects that encode the output stream of the build operation. We need to
 * parse the JSON and extract/decode the build operation output stream.
 *
 * Sample REST response:
	{"stream": "Step 1..."}
	{"stream": "..."}
	{"error": "Error...", "errorDetail": {"code": 123, "message": "Error..."}}
	
 * Another sample:
	{"stream":"Step 1 : FROM centos\n"}
	{"stream":" ---\u003e 968790001270\n"}
	{"stream":"Step 2 : RUN echo moo \u003e oink\n"}
	{"stream":" ---\u003e Using cache\n"}
	{"stream":" ---\u003e cb0948362f97\n"}
	{"stream":"Successfully built cb0948362f97\n"}
	
 * Another sample:
	{"status":"Pulling from library/alpine","id":"latest"}
 */
func extractBuildOutputFromRESTResponse(restResponse string) (string, error) {
	
	fmt.Println("extractBuildOutputFromRESTResponse: A")  // debug
	
	var reader = bufio.NewReader(strings.NewReader(restResponse))
	
	var output = ""
	for {
		var lineBytes []byte
		var isPrefix bool
		var err error
		lineBytes, isPrefix, err = reader.ReadLine()
		fmt.Println("extractBuildOutputFromRESTResponse: A.1")  // debug
		if err == io.EOF { break }
		if err != nil { return "", err }
		fmt.Println("extractBuildOutputFromRESTResponse: A.2")  // debug
		if isPrefix { fmt.Println("Warning - only part of string was read") }
		
		var obj interface{}
		err = json.Unmarshal(lineBytes, &obj)
		if err != nil { return "", err }
		fmt.Println("extractBuildOutputFromRESTResponse: A.3")  // debug
		
		var isType bool
		var msgMap map[string]interface{}
		msgMap, isType = obj.(map[string]interface{})
		if ! isType { return "", utilities.ConstructServerError(
			"Unexpected format for json build output: " + string(lineBytes))
		}
		fmt.Println("extractBuildOutputFromRESTResponse: A.4")  // debug
		var value string
		obj = msgMap["stream"]
		value, isType = obj.(string)
		if obj == nil {
			obj = msgMap["status"]
			value, isType = obj.(string)
			if obj == nil {
				// Check for error message.
				obj = msgMap["error"]
				if obj == nil { return "", utilities.ConstructServerError(
					"Unexpected field: " + string(lineBytes))
				}
				fmt.Println("extractBuildOutputFromRESTResponse: A.4.1")  // debug
				
				// Error message found.
				var errMsg string
				errMsg, isType = obj.(string)
				if ! isType { return "", utilities.ConstructServerError(
					"Unexpected data in json error value; line: " + string(lineBytes))
				}
				fmt.Println("extractBuildOutputFromRESTResponse: A.4.2")  // debug
	
				// Get error detail message.
				obj = msgMap["errorDetail"]
				if obj == nil { return "", utilities.ConstructServerError(
					"Unexpected JSON field in errorDetail message: " + string(lineBytes))
				}
				fmt.Println("extractBuildOutputFromRESTResponse: A.4.3")  // debug

				var errDetailMsgJSON map[string]interface{}
				errDetailMsgJSON, isType = obj.(map[string]interface{})
				if ! isType { return "", utilities.ConstructServerError(
					"Unexpected data in json errorDetail value; line: " + string(lineBytes))
				}
				fmt.Println("extractBuildOutputFromRESTResponse: A.4.4")  // debug

				obj = errDetailMsgJSON["message"]
				if obj == nil { return "", utilities.ConstructServerError(
					"No message field in errorDetail message: " + string(lineBytes))
				}
				fmt.Println("extractBuildOutputFromRESTResponse: A.4.5")  // debug

				var errDetailMsg string
				errDetailMsg, isType = obj.(string)
				
				return "", utilities.ConstructUserError(errMsg + "; " + errDetailMsg)
			}
		}
		fmt.Println("extractBuildOutputFromRESTResponse: A.5")  // debug
		if ! isType { return "", utilities.ConstructServerError(
			"Unexpected type in json field value: " + reflect.TypeOf(obj).String())
		}

		output = output + value
	}
	fmt.Println("extractBuildOutputFromRESTResponse: B")  // debug
	
	return output, nil
}
