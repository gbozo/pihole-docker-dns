.PHONY: build run test clean docker-build docker-run docker-clean

build:
	go mod download
	go build -o pihole-docker-dns .

run: build
	./pihole-docker-dns

docker-build:
	docker compose build

docker-run:
	docker compose up --build

docker-clean:
	docker compose down --rmi local

test:
	go test -v ./...

clean:
	rm -f pihole-docker-dns

deps:
	go mod download
	go mod tidy