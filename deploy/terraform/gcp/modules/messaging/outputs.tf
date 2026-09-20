output "topic" {
  description = "Topic the subscription reads from"
  value       = var.create_topic ? google_pubsub_topic.source[0].name : data.google_pubsub_topic.source[0].name
}

output "subscription" {
  description = "Push subscription feeding the ingest service"
  value       = google_pubsub_subscription.ingest.name
}

output "dead_letter_topic" {
  description = "Topic holding messages that failed every delivery"
  value       = google_pubsub_topic.dead_letter.name
}
