package storage

import "time"

type bucketResource struct {
	Kind           string    `json:"kind"`
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	ProjectNumber  string    `json:"projectNumber,omitempty"`
	Location       string    `json:"location,omitempty"`
	StorageClass   string    `json:"storageClass,omitempty"`
	TimeCreated    time.Time `json:"timeCreated"`
	Updated        time.Time `json:"updated"`
	Metageneration string    `json:"metageneration,omitempty"`
	Etag           string    `json:"etag,omitempty"`
}

type bucketsList struct {
	Kind  string           `json:"kind"`
	Items []bucketResource `json:"items"`
}

type objectResource struct {
	Kind           string    `json:"kind"`
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Bucket         string    `json:"bucket"`
	Generation     string    `json:"generation"`
	Metageneration string    `json:"metageneration"`
	ContentType    string    `json:"contentType,omitempty"`
	Size           string    `json:"size"`
	MD5Hash        string    `json:"md5Hash,omitempty"`
	CRC32C         string    `json:"crc32c,omitempty"`
	Etag           string    `json:"etag,omitempty"`
	TimeCreated    time.Time `json:"timeCreated"`
	Updated        time.Time `json:"updated"`
	StorageClass   string    `json:"storageClass,omitempty"`
	SelfLink       string    `json:"selfLink,omitempty"`
	MediaLink      string    `json:"mediaLink,omitempty"`
}

type objectsList struct {
	Kind  string           `json:"kind"`
	Items []objectResource `json:"items"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
