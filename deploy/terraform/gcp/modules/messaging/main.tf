data "google_project" "this" {
  project_id = var.project_id
}

# The Pub/Sub service agent moves messages to the dead-letter topic and acks
# them on the subscription, so it needs rights on both.
locals {
  pubsub_agent = "serviceAccount:service-${data.google_project.this.number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_pubsub_topic" "source" {
  count = var.create_topic ? 1 : 0
  name  = var.topic_name
}

data "google_pubsub_topic" "source" {
  count = var.create_topic ? 0 : 1
  name  = var.topic_name
}

resource "google_pubsub_topic" "dead_letter" {
  name = "${var.name}-ingest-dlq"
}

resource "google_pubsub_subscription" "ingest" {
  name  = "${var.name}-ingest"
  topic = var.create_topic ? google_pubsub_topic.source[0].id : data.google_pubsub_topic.source[0].id

  # Long enough for a multi-megabyte parse plus a BigQuery append.
  ack_deadline_seconds = var.ack_deadline_seconds

  push_config {
    push_endpoint = var.push_endpoint

    oidc_token {
      service_account_email = var.push_service_account
      # An independently chosen constant, not var.push_endpoint: deriving it
      # from the service's own URL would make the root module read the
      # service's URL to configure the service's own environment, a
      # dependency cycle Terraform cannot resolve. Cloud Run's
      # custom_audiences is set to this same value so the two agree.
      audience = var.audience
    }
  }

  retry_policy {
    minimum_backoff = "10s"
    maximum_backoff = "600s"
  }

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.dead_letter.id
    max_delivery_attempts = var.max_delivery_attempts
  }

  depends_on = [
    google_pubsub_topic_iam_member.dlq_publisher,
  ]
}

resource "google_pubsub_topic_iam_member" "dlq_publisher" {
  topic  = google_pubsub_topic.dead_letter.name
  role   = "roles/pubsub.publisher"
  member = local.pubsub_agent
}

resource "google_pubsub_subscription_iam_member" "agent_subscriber" {
  subscription = google_pubsub_subscription.ingest.name
  role         = "roles/pubsub.subscriber"
  member       = local.pubsub_agent
}

# Nothing consumes the dead-letter topic automatically; this subscription is
# what keeps the messages retrievable while someone investigates.
resource "google_pubsub_subscription" "dead_letter" {
  name                       = "${var.name}-ingest-dlq"
  topic                      = google_pubsub_topic.dead_letter.id
  message_retention_duration = "604800s" # 7 days
}
