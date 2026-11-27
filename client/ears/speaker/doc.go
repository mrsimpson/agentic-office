// Package speaker tells the voice of the owner from other voices (dec-voice-print). It is part of
// the building block Ears (bb-ears).
//
// The model is WeSpeaker ResNet34 (voxceleb, large-margin fine-tuned): log-mel filterbanks go
// through four stages of residual blocks, statistics pooling gives the mean and the deviation over
// time, and a linear layer gives 256 numbers per voice. The forward pass is written in plain Go
// like the NER model: no cgo and no native library. The weights are read from the ONNX file of
// the model (onnx.go), the features are Kaldi filterbanks (fbank.go), the model is in model.go.
//
// The network runs once over a whole sentence (Model.Map). The embedding of any part of it is
// then cheap (Model.Embed), so the gate (gate.go) scores overlapping windows of 1.5 s every 0.5 s
// and decides frame by frame which stretches are the owner's voice. Its limits come from the
// spike on German speech through simulated loudspeakers and one real voice; they are constants of
// the model and not settings. The voice print (print.go) is one JSON file with 256 numbers, never
// sent anywhere.
//
// The output of the Go code equals the output of the ONNX model run by ONNX Runtime to four
// decimals (spike, 2026-11-27).
package speaker
