// Copyright 2017 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may not
// use this file except in compliance with the License. A copy of the
// License is located at
//
// http://aws.amazon.com/apache2.0/
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

// Package s3resource implements the methods to access resources from s3
package s3resource

import (
	"github.com/aws/amazon-ssm-agent/agent/appconfig"
	"github.com/aws/amazon-ssm-agent/agent/fileutil"
	"github.com/aws/amazon-ssm-agent/agent/fileutil/artifact"
	"github.com/aws/amazon-ssm-agent/agent/jsonutil"
	"github.com/aws/amazon-ssm-agent/agent/log"
	"github.com/aws/amazon-ssm-agent/agent/plugins/executecommand/filemanager"
	"github.com/aws/amazon-ssm-agent/agent/plugins/executecommand/remoteresource"
	"github.com/aws/amazon-ssm-agent/agent/s3util"

	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// S3Resource is a struct for the remote resource of type git
type S3Resource struct {
	Info     S3Info
	s3Object s3util.AmazonS3URL
}

// S3Info represents the locationInfo type sent by runcommand
type S3Info struct {
	Path string `json:"path"`
}

// NewS3Resource is a constructor of type GitResource
func NewS3Resource(log log.T, info string) (s3 *S3Resource, err error) {
	var s3Info S3Info
	var input artifact.DownloadInput

	if s3Info, err = parseLocationInfo(info); err != nil {
		return nil, fmt.Errorf("s3 url parsing failed. %v", err)
	}

	input.SourceURL = s3Info.Path
	return &S3Resource{
		Info: s3Info,
	}, nil
}

// parseLocationInfo unmarshals the information in locationInfo of type GitInfo and returns it
func parseLocationInfo(locationInfo string) (s3Info S3Info, err error) {

	if err = jsonutil.Unmarshal(locationInfo, &s3Info); err != nil {
		return s3Info, fmt.Errorf("Location Info could not be unmarshalled for location type S3. Please check JSON format of locationInfo")
	}

	return
}

// Download calls download to pull down files or directory from s3
func (s3 *S3Resource) Download(log log.T, filesys filemanager.FileSystem, entireDir bool, destinationDir string) (err error) {
	var fileURL *url.URL
	var folders []string
	var localFilePath string
	if destinationDir == "" {
		destinationDir = appconfig.DownloadRoot
	}
	log.Info("Downloading S3 artifacts")
	if fileURL, err = s3.getSourceURL(log, entireDir); err != nil {
		return err
	}
	log.Debug("File URL - ", fileURL.String())
	// Create an object for the source URL. This can be used to list the objects in the folder
	// when entireDir is true
	s3.s3Object = s3util.ParseAmazonS3URL(log, fileURL)
	log.Debug("S3 object - ", s3.s3Object.String())
	if entireDir {
		if folders, err = dep.ListS3Objects(log, s3.s3Object); err != nil {
			return err
		}
	} else {
		// In case of a file download, append the filename to folders
		folders = append(folders, s3.s3Object.Key)
	}

	// The URL till the bucket name will be concatenated with the prefix in the loop
	// responsible for download
	bucketURL := s3.getS3BucketURLString()
	log.Debug("S3 bucket URL -", bucketURL)

	for _, files := range folders {
		log.Debug("Name of file - ", files)
		var input artifact.DownloadInput
		if !isPathType(files) { //Only download in case the URL is a file
			localFilePath = fileutil.BuildPath(destinationDir, s3.s3Object.Bucket, filepath.Dir(files))

			// Obtain the full URL for the file before download
			input.DestinationDirectory = localFilePath
			input.SourceURL = bucketURL + files

			log.Debug("SourceURL ", input.SourceURL)
			downloadOutput, err := dep.Download(log, input)
			if err != nil {
				return err
			}

			if err = filemanager.RenameFile(log, filesys, downloadOutput.LocalFilePath, filepath.Base(files)); err != nil {
				return fmt.Errorf("Something went wrong when trying to access downloaded content. It is "+
					"possible that the content was not downloaded because the path provided is wrong. %v", err)
			}
		}
	}
	return nil
}

// PopulateResourceInfo set the member variables of ResourceInfo
func (s3 *S3Resource) PopulateResourceInfo(log log.T, destinationDir string, entireDir bool) remoteresource.ResourceInfo {
	var resourceInfo remoteresource.ResourceInfo

	//if destination directory is not specified, specify the directory
	if destinationDir == "" {
		destinationDir = appconfig.DownloadRoot
	}

	if entireDir {
		resourceInfo.StarterFile = filepath.Base(s3.Info.Path)
		resourceInfo.LocalDestinationPath = fileutil.BuildPath(destinationDir, s3.s3Object.Bucket, s3.s3Object.Key, resourceInfo.StarterFile)
		resourceInfo.ResourceExtension = filepath.Ext(resourceInfo.StarterFile)
		resourceInfo.TypeOfResource = remoteresource.Script //Because EntireDirectory option is valid only for scripts
		resourceInfo.EntireDir = true
	} else {
		resourceInfo.LocalDestinationPath = fileutil.BuildPath(destinationDir, s3.s3Object.Bucket, s3.s3Object.Key)
		resourceInfo.StarterFile = filepath.Base(resourceInfo.LocalDestinationPath)
		resourceInfo.ResourceExtension = filepath.Ext(resourceInfo.StarterFile)
		if strings.Compare(resourceInfo.ResourceExtension, remoteresource.JSONExtension) == 0 || strings.Compare(resourceInfo.ResourceExtension, remoteresource.YAMLExtension) == 0 {
			resourceInfo.TypeOfResource = remoteresource.Document
		} else { // It is assumed that the file is a script if the extension is not JSON nor YAML
			resourceInfo.TypeOfResource = remoteresource.Script
		}
	}
	return resourceInfo
}

// ValidateLocationInfo ensures that the required parameters of Location Info are specified
func (s3 *S3Resource) ValidateLocationInfo() (valid bool, err error) {
	// Path is a mandatory input
	if s3.Info.Path == "" {
		return false, errors.New("S3 source path in LocationType must be specified")
	}

	return true, nil
}

// getDirectoryURLString returns the URL of the directory in which the path lies
func (s3 *S3Resource) getDirectoryURLString() string {
	splitSourceURL := strings.SplitAfter(s3.Info.Path, "/")
	// get all but the last element. This will retain the / after the directory
	dirSourceURL := splitSourceURL[:len(splitSourceURL)-1]
	return strings.Join(dirSourceURL, "")
}

// getS3BucketURLString returns the URL up to the bucket name
func (s3 *S3Resource) getS3BucketURLString() string {
	bucketURL := strings.SplitAfter(s3.Info.Path, s3.s3Object.Bucket)
	URL := bucketURL[0]
	return URL + "/"
}

// getSourceURL determines the source URL when entire directory is true or not
func (s3 *S3Resource) getSourceURL(log log.T, entireDir bool) (*url.URL, error) {
	var sourceURL string
	if entireDir {
		if isPathType(s3.Info.Path) {
			return nil, errors.New("Could not download from S3. Please provide path to file with extention.")
		}
		sourceURL = s3.getDirectoryURLString()
	} else {
		sourceURL = s3.Info.Path
	}
	log.Info("SourceURL - ", sourceURL)
	return url.Parse(sourceURL)
}

// isPathType returns if the URL is of path type
func isPathType(folderName string) bool {
	lastCharacter := folderName[len(folderName)-1:]
	if lastCharacter == "/" {
		return true
	}
	return false
}
