PROJECT := dll_version.csproj

build:
	dotnet publish $(PROJECT) \
	  -r osx-arm64 \
	  -c Release \
	  -o ../../build/dll_version \
	  -p:PublishAot=false --self-contained true

build_linux_amd64:
	dotnet publish $(PROJECT) \
	  -r linux-x64 \
	  -c Release \
	  -o ../../build/dll_version \
	  -p:PublishAot=true --self-contained true

run:
	dotnet run --project $(PROJECT) -- $(dll)

clean:
	rm -rf ./bin ./obj

.PHONY: build run clean
