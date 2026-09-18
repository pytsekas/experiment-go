# Survives `make aws-teardown-all` on purpose (storage is cents per month and it
# saves a rebuild). `terraform destroy` removes it, images included.
resource "aws_ecr_repository" "app" {
  name                 = var.name
  image_tag_mutability = "MUTABLE" # :latest is re-pushed on every build
  force_delete         = true      # destroy works even with images present

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_lifecycle_policy" "keep_recent" {
  repository = aws_ecr_repository.app.name

  policy = jsonencode({
    rules = [{
      rulePriority = 1
      description  = "Keep the 10 most recent images"
      selection = {
        tagStatus   = "any"
        countType   = "imageCountMoreThan"
        countNumber = 10
      }
      action = { type = "expire" }
    }]
  })
}
