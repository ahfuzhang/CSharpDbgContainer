
# make docker-build ver=6.0
# make docker-build ver=8.0
# make docker-build ver=10.0
DOTNET_DOCKER_IMAGE ?= ahfuzhang/csharp-dbg-all-in-one:dotnet10
TRACEME_RUNTIME ?= linux-x64
TRACEME_OUTPUT_DIR ?= ./build/examples/TraceMe/linux/amd64/

docker-build:
	docker build --platform linux/amd64 \
	  --build-arg DOTNET_VERSION=$(ver) \
	  -t ahfuzhang/csharp-dbg-all-in-one:dotnet$(word 1,$(subst ., ,$(ver))) -f ./Dockerfile .

./build/speedscope/.unpacked: ./build/speedscope-1.24.0.zip
	mkdir -p build
	if command -v unzip >/dev/null 2>&1; then \
	  unzip -oq $< -d ./build; \
	else \
	  python3 -m zipfile -e $< ./build; \
	fi
	touch $@

./build/speedscope-1.24.0.zip:
	mkdir -p $(dir $@)
	wget -O $@ https://github.com/jlfwong/speedscope/releases/download/v1.24.0/speedscope-1.24.0.zip

download: ./build/speedscope/.unpacked
	@echo "ok"

build: download
	mkdir -p build
	go build -o ./build/debugadmin .

build-linux-amd64: download
	mkdir -p build
	GOOS=linux GOARCH=amd64 go build -o ./build/debugadmin-linux-amd64 .

run:
	./build/debugadmin -startup=/Users/ahfu/code/github.com/ahfuzhang/daily_coding/csharp/cmd_line/build/Debug/osx/arm64/cmd_line.dll

run-in-docker:
	docker run -it --rm --name=csharp_debug_admin_test \
	--platform linux/amd64 \
	--network="host" \
	--cpuset-cpus="2" \
	-m 512m \
	-p 8089:8089 \
	-v "/Users/ahfu/code/github.com/ahfuzhang/QiWa/build/code-snippets/Http1EchoServer/linux/amd64/":/app/ \
	-v ./build/:/debug_admin/ \
	-w /app/ \
	ahfuzhang/csharp-dbg-all-in-one:dotnet10 \
		/debug_admin/debugadmin-linux-amd64 -startup="/app/Http1EchoServer.dll --http1.port=8081"

build-example-traceme: download
ifeq ($(shell uname -s),Linux)
	dotnet publish ./examples/TraceMe/TraceMe.csproj \
		-c Release -r $(TRACEME_RUNTIME) \
		-p:PublishAot=false \
		-p:StripSymbols=false \
		-p:EventSourceSupport=true \
		-p:PublishReadyToRun=true \
		--self-contained false \
		-o $(TRACEME_OUTPUT_DIR)
else
	docker run --rm --platform linux/amd64 \
		-u $$(id -u):$$(id -g) \
		-e HOME=/tmp \
		-e DOTNET_CLI_HOME=/tmp \
		-e NUGET_PACKAGES=/tmp/.nuget/packages \
		-v "$(CURDIR)":/work \
		-w /work \
		$(DOTNET_DOCKER_IMAGE) \
		dotnet publish ./examples/TraceMe/TraceMe.csproj \
			-c Release -r $(TRACEME_RUNTIME) \
			-p:PublishAot=false \
			-p:StripSymbols=false \
			-p:EventSourceSupport=true \
			-p:PublishReadyToRun=true \
			--self-contained false \
			-o $(TRACEME_OUTPUT_DIR)
endif

build-example-traceme-on-docker:
	docker run --rm --platform linux/amd64 \
		-u $$(id -u):$$(id -g) \
		-e HOME=/tmp \
		-e DOTNET_CLI_HOME=/tmp \
		-e NUGET_PACKAGES=/tmp/.nuget/packages \
		-v "$(CURDIR)":/work \
		-w /work \
		$(DOTNET_DOCKER_IMAGE) \
		dotnet publish ./examples/TraceMe/TraceMe.csproj \
			-c Release -r $(TRACEME_RUNTIME) \
			-p:PublishAot=false \
			-p:StripSymbols=false \
			-p:EventSourceSupport=true \
			-p:PublishReadyToRun=true \
			--self-contained false \
			-o $(TRACEME_OUTPUT_DIR)

run-example-traceme:
	docker run -it --rm --name=csharp_debug_admin_test \
	--platform linux/amd64 \
	-u $$(id -u):$$(id -g) \
	--cpuset-cpus="2" \
	-m 512m \
	-p 8089:8089 \
	-v "$(TRACEME_OUTPUT_DIR)":/app/:rw \
	-w /app/ \
	$(DOTNET_DOCKER_IMAGE) \
		/app/TraceMe -port=8089 -cores=1

run-example-traceme-direct:
	dotnet run --project ./examples/TraceMe/TraceMe.csproj -port=8089 -cores=1
	# ./build/examples/TraceMe/linux/amd64/TraceMe -port=8089 -cores=1


.PHONY: build build-linux-amd64 download build-example-traceme


wrk:
	wrk -c 10 -t 1 -d 120s http://127.0.0.1:8089/echo?msg=def --latency
