output "dataset_id" {
  description = "Dataset holding the readings"
  value       = google_bigquery_dataset.readings.dataset_id
}

output "table_id" {
  description = "Append-only readings table"
  value       = google_bigquery_table.readings.table_id
}

output "view_id" {
  description = "Deduplicating view to query"
  value       = google_bigquery_table.readings_current.table_id
}
