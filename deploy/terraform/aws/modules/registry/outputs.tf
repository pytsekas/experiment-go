output "repository_url" {
  description = "Image path without a tag (the Makefile appends :VERSION)"
  value       = aws_ecr_repository.app.repository_url
}
