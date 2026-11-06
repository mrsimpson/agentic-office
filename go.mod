module github.com/mrsimpson/agentic-office

go 1.27

replace github.com/ggerganov/whisper.cpp/bindings/go => ./third_party/whisper.cpp/bindings/go

require (
	connectrpc.com/connect/v2 v2.0.2
	github.com/ggerganov/whisper.cpp/bindings/go v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	google.golang.org/protobuf v1.36.12
)
