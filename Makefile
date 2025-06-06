templ:
	go tool templ generate --watch --proxy="http://localhost:8090" --open-browser=false
server:
	go tool air
tailwind:
	tailwindcss -i ./internal/server/assets/css/input.css -o ./internal/server/assets/css/output.css --watch
dev:
	make -j3 tailwind templ server
