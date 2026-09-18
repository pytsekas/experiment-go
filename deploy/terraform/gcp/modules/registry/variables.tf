variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "Region of the Artifact Registry repository"
  type        = string
}

variable "repository_id" {
  description = "Artifact Registry repository id for container images"
  type        = string
}
