# ---------------------------------------------------------
# Stage 1: Build the application
# ---------------------------------------------------------
# We start with an official Go image that includes all the tools required to compile our code.
# Think of this stage as a factory that puts together the application.
FROM golang:1.27-alpine AS builder

# We set the working directory inside the container. All subsequent commands will happen here.
WORKDIR /app

# We copy the module files first. Docker is smart: if these files haven't changed, 
# it won't re-download the dependencies, saving us a lot of time on future builds.
COPY go.mod go.sum ./

# Download all the required external packages (like chi and modernc.org/sqlite).
RUN go mod download

# Now we copy the rest of our application's actual source code into the factory.
COPY . .

# We compile the application into a single executable program named 'server'.
# - CGO_ENABLED=0 ensures the program doesn't rely on external C libraries, making it highly portable.
# - GOOS=linux ensures it is built to run on the Linux system we'll use in the final stage.
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

# ---------------------------------------------------------
# Stage 2: Create the final lightweight image
# ---------------------------------------------------------
# Now that we have our finished program, we don't need the heavy compilation tools anymore.
# We start a brand new stage with a tiny Alpine Linux image to keep our final product small and fast.
FROM alpine:latest

# We add basic root certificates so our application can make secure web requests (HTTPS) if it ever needs to.
RUN apk --no-cache add ca-certificates

WORKDIR /app

# We grab ONLY the compiled 'server' program from the "builder" factory stage and bring it here.
COPY --from=builder /app/server .

# We tell Docker that our application will be listening for traffic on port 8080.
EXPOSE 8080

# Finally, we specify the default command that should run when someone starts this container.
CMD ["./server"]
