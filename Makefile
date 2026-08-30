# Build and install `topic`.
#
# Always build through here (or scripts/build.sh directly). A bare `go build` produces
# a binary whose code-signing identifier is `a.out`, which macOS cannot key a
# permission decision against — so every TCC dialog naming `topic` comes back forever,
# no matter what you click. See README, "Why the binary is code-signed".

TOPIC_IDENTIFIER    ?= io.github.verveguy.topic
TOPIC_SIGN_IDENTITY ?= -
export TOPIC_IDENTIFIER
export TOPIC_SIGN_IDENTITY

.PHONY: all build test install doctor clean

all: build

# Compile and (on macOS) sign. Fails loudly if the signature did not take.
build:
	@scripts/build.sh

# The black-box CLI tests drive bin/topic, so build first.
test: build
	go test ./test/...

install: build
	./install.sh

# What macOS actually thinks this binary is.
doctor: build
	@bin/topic doctor

clean:
	rm -f bin/topic
