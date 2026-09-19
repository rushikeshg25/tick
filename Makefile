.PHONY: test race bench vet cover clean

test:
	go test ./...

race:
	go test -race -count=1 ./...

bench:
	go test -run '^$$' -bench . -benchmem ./...

vet:
	go vet ./...

cover:
	go test -coverprofile=coverage.txt ./...
	go tool cover -html=coverage.txt -o coverage.html

clean:
	rm -rf bin coverage.txt coverage.html
