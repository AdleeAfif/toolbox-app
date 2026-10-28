IMAGE ?= docker.io/adleeafif/toolbox-app
TAG ?= dev
PLATFORMS ?= linux/arm64,linux/amd64

.PHONY: run test image deploy

run:
	go run ./cmd/server

test:
	go test ./...

image:
	docker buildx build --platform $(PLATFORMS) -t $(IMAGE):$(TAG) --push .

deploy:
	kubectl apply -k deploy/k8s
