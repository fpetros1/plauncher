build:
	mkdir -p dist
	rm -f dist/*
	go build -o dist/plauncher plauncher.go file-utils.go steam.go

install:
	mkdir -p /opt/plauncher
	cp dist/plauncher /opt/plauncher/plauncher
	ln -fs /opt/plauncher/plauncher /usr/bin/plauncher
