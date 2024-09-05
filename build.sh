# create a new manifest: 
podman manifest create ocfl-tools-image

# build with buildx
podman buildx build --platform linux/arm64,linux/amd64  \
	-t srerickson/ocfl-tools:latest \
	--format docker \
	--manifest ocfl-tools-image .

# push to docker
podman manifest push ocfl-tools-image "docker://srerickson/ocfl-tools:latest"

# remove the manifest
podman manifest rm ocfl-tools-image