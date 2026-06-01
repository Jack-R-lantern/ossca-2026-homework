.PHONY: build run test clean

build:
	go build -o app .

run: build
	./app

test:
	curl -X POST http://localhost:8080/unshare/netns \
		-H "Content-Type: application/json" \
		-d '{"path": "/bin/sleep", "args": ["30"]}'

clean:
	rm -f app