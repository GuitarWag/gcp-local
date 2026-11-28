terraform {
  required_version = ">= 1.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "= 8.10.0"
    }
  }
}

variable "port" {
  type    = number
  default = 8080
}

variable "project" {
  type    = string
  default = "test-project"
}

provider "google" {
  project = var.project
  region  = "us-central1"

  storage_custom_endpoint        = "http://localhost:${var.port}/storage/v1/"
  pubsub_custom_endpoint         = "http://localhost:${var.port}/v1/"
  secret_manager_custom_endpoint = "http://localhost:${var.port}/v1/"
  cloud_run_custom_endpoint      = "http://localhost:${var.port}/v1/"
}

resource "google_cloud_run_service" "tf-accept-run" {
  name     = "tf-accept-run"
  location = "us-central1"

  metadata {
    namespace = "default"
  }

  template {
    spec {
      containers {
        image = "us-docker.pkg.dev/cloudrun/container/hello"
      }
    }
  }
}
