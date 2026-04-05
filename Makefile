.PHONY: dev up down reset build logs generate

## Start development stack (hot-reload)
dev:
	docker compose -f docker/docker-compose-dev.yml up --build -V

## Start production stack
up:
	docker compose -f docker/docker-compose.yml up --build -d

## Stop all containers
down:
	docker compose -f docker/docker-compose-dev.yml down
	docker compose -f docker/docker-compose.yml down

## Full teardown + clean restart (dev)
reset:
	docker compose -f docker/docker-compose-dev.yml down -v
	docker compose -f docker/docker-compose-dev.yml up --build

## Build production image only
build:
	docker compose -f docker/docker-compose.yml build

## Follow logs (dev)
logs:
	docker compose -f docker/docker-compose-dev.yml logs -f openscape-app

## Generate templ files locally (requires templ installed)
generate:
	templ generate
