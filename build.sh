# create a new manifest: 
podman manifest create ocfl-tools-image

# build with buildx
podman buildx build --rm --platform linux/arm64,linux/amd64  \
	--build-arg OCFLTOOLS_VERSION=$(cat VERSION) \
	--build-arg OCFLTOOLS_BUILDTIME=$(date +"%Y%m%d.%H%M%S") \
	-t srerickson/ocfl-tools:latest \
	--manifest ocfl-tools-image .

# # push to docker
podman manifest push ocfl-tools-image "docker://srerickson/ocfl-tools:latest"

# # remove the manifest
podman manifest rm ocfl-tools-image