PROJECT := TraceMeV2.csproj

DOTNET:=~/.dotnet/dotnet

build:
	$(DOTNET) build $(PROJECT)

run:
	$(DOTNET) run --project $(PROJECT) -- -port=8089 -cores=1

.PHONY: build run
