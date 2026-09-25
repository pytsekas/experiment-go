resource "google_bigquery_dataset" "readings" {
  dataset_id  = var.dataset_id
  location    = var.region
  description = "Energy consumption interval readings ingested from Pub/Sub"

  labels = {
    app = var.name
  }
}

# Rows are append-only: corrections and Pub/Sub redeliveries both arrive as new
# rows and are resolved by the view below, never by updating in place.
resource "google_bigquery_table" "readings" {
  dataset_id          = google_bigquery_dataset.readings.dataset_id
  table_id            = var.table_id
  deletion_protection = true

  time_partitioning {
    type  = "DAY"
    field = "interval_start"
  }

  clustering = ["metering_point_id", "direction", "measure"]

  schema = jsonencode([
    { name = "metering_point_id", type = "STRING", mode = "REQUIRED" },
    { name = "interval_start", type = "TIMESTAMP", mode = "REQUIRED" },
    { name = "resolution_sec", type = "INTEGER", mode = "REQUIRED" },
    { name = "value", type = "FLOAT", mode = "REQUIRED" },
    { name = "unit", type = "STRING", mode = "REQUIRED" },
    { name = "quality", type = "STRING", mode = "REQUIRED" },
    { name = "direction", type = "STRING", mode = "REQUIRED" },
    # gross is what the meter registered; net is the same interval after the
    # source netted the directions. Sources do not net by subtraction, so net
    # is carried as its own row rather than derived in SQL.
    { name = "measure", type = "STRING", mode = "REQUIRED" },
    { name = "source_message_id", type = "STRING", mode = "REQUIRED" },
    { name = "ingested_at", type = "TIMESTAMP", mode = "REQUIRED" },
  ])
}

# The read surface. A later correction wins because it was ingested later.
resource "google_bigquery_table" "readings_current" {
  dataset_id          = google_bigquery_dataset.readings.dataset_id
  table_id            = "${var.table_id}_current"
  deletion_protection = false

  view {
    use_legacy_sql = false
    query          = <<-SQL
      SELECT * EXCEPT(rn) FROM (
        SELECT *, ROW_NUMBER() OVER (
          PARTITION BY metering_point_id, interval_start, direction, measure
          ORDER BY ingested_at DESC) AS rn
        FROM `${var.project_id}.${google_bigquery_dataset.readings.dataset_id}.${google_bigquery_table.readings.table_id}`
      ) WHERE rn = 1
    SQL
  }
}

resource "google_bigquery_dataset_iam_member" "writer" {
  dataset_id = google_bigquery_dataset.readings.dataset_id
  role       = "roles/bigquery.dataEditor"
  member     = "serviceAccount:${var.writer_service_account}"
}
