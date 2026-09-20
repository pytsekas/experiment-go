variable "project_id" {
  description = "GCP project that owns the dataset"
  type        = string
}

variable "region" {
  description = "Dataset location; must match where the service runs"
  type        = string
  default     = "europe-north1"
}

variable "name" {
  description = "Application name, used as a label"
  type        = string
}

variable "dataset_id" {
  description = "BigQuery dataset holding the readings table"
  type        = string
  default     = "energy"
}

variable "table_id" {
  description = "Table receiving the parsed readings"
  type        = string
  default     = "readings"
}

variable "writer_service_account" {
  description = "Service account email allowed to append rows"
  type        = string
}
