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

resource "google_storage_bucket" "tf-accept-bucket" {
  name          = "tf-accept-bucket"
  location      = "US"
  storage_class = "STANDARD"
}

resource "google_pubsub_topic" "tf-accept-topic" {
  name = "tf-accept-topic"
}

resource "google_pubsub_subscription" "tf-accept-sub" {
  name  = "tf-accept-sub"
  topic = google_pubsub_topic.tf-accept-topic.id

  push_config {
    push_endpoint = "https://example.internal/hook"

    attributes = {
      x-goog-version = "v1"
    }
  }

  ack_deadline_seconds = 10
}

resource "google_secret_manager_secret" "tf-accept-secret" {
  secret_id = "tf-accept-secret"

  replication {
    auto {}
  }
}
