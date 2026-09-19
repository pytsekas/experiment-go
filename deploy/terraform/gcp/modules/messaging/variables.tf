variable "project_id" {
  description = "GCP project that owns the subscription"
  type        = string
}

variable "name" {
  description = "Application name; prefixes the resources"
  type        = string
}

variable "topic_name" {
  description = "Upstream topic carrying the XML documents"
  type        = string
}

variable "topic_project" {
  description = "GCP project that owns the upstream topic, when it differs from project_id (e.g. another system's project); empty uses the provider's configured project"
  type        = string
  default     = ""
}

variable "create_topic" {
  description = "Create the topic here (development) instead of referencing one another system owns"
  type        = bool
  default     = false
}

variable "push_endpoint" {
  description = "Full URL of the ingest endpoint, including the path"
  type        = string
}

variable "push_service_account" {
  description = "Service account Pub/Sub signs the OIDC token with"
  type        = string
}

variable "audience" {
  description = "OIDC audience Pub/Sub requests and the ingest service validates; an independently chosen constant (not derived from push_endpoint) so the service's own custom_audiences configuration doesn't depend on its own URL"
  type        = string
}

variable "ack_deadline_seconds" {
  description = "How long the handler has to parse and store one message"
  type        = number
  default     = 60
}

variable "max_delivery_attempts" {
  description = "Deliveries before a message goes to the dead-letter topic"
  type        = number
  default     = 5
}
