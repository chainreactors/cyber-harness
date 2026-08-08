//go:build record_ffmpeg && cgo && (windows || linux)

package record

import (
	"errors"
	"fmt"

	"github.com/asticode/go-astiav"
)

type videoEncoder struct {
	formatContext  *astiav.FormatContext
	codecContext   *astiav.CodecContext
	stream         *astiav.Stream
	ioContext      *astiav.IOContext
	scaler         *astiav.SoftwareScaleContext
	frame          *astiav.Frame
	packet         *astiav.Packet
	headerWritten  bool
	trailerWritten bool
}

func openVideoEncoder(path string, fps, width, height int, sourceFormat astiav.PixelFormat) (*videoEncoder, error) {
	encoder := &videoEncoder{}
	var err error
	encoder.formatContext, err = astiav.AllocOutputFormatContext(nil, "mp4", path)
	if err != nil {
		return nil, fmt.Errorf("allocate MP4 output: %w", err)
	}
	if encoder.formatContext == nil {
		return nil, fmt.Errorf("allocate MP4 output")
	}
	codec := astiav.FindEncoderByName("libx264")
	if codec == nil {
		encoder.close()
		return nil, fmt.Errorf("FFmpeg encoder libx264 is unavailable")
	}
	encoder.codecContext = astiav.AllocCodecContext(codec)
	if encoder.codecContext == nil {
		encoder.close()
		return nil, fmt.Errorf("allocate libx264 context")
	}
	encoder.codecContext.SetWidth(width)
	encoder.codecContext.SetHeight(height)
	encoder.codecContext.SetPixelFormat(astiav.PixelFormatYuv420P)
	encoder.codecContext.SetTimeBase(astiav.NewRational(1, fps))
	encoder.codecContext.SetFramerate(astiav.NewRational(fps, 1))
	encoder.codecContext.SetGopSize(fps * 2)
	encoder.codecContext.SetMaxBFrames(2)
	if encoder.formatContext.OutputFormat().Flags().Has(astiav.IOFormatFlagGlobalheader) {
		encoder.codecContext.SetFlags(encoder.codecContext.Flags().Add(astiav.CodecContextFlagGlobalHeader))
	}
	codecOpts := astiav.NewDictionary()
	if err := codecOpts.Set("preset", "veryfast", astiav.NewDictionaryFlags()); err != nil {
		codecOpts.Free()
		encoder.close()
		return nil, fmt.Errorf("set encoder preset: %w", err)
	}
	if err := codecOpts.Set("crf", "23", astiav.NewDictionaryFlags()); err != nil {
		codecOpts.Free()
		encoder.close()
		return nil, fmt.Errorf("set encoder quality: %w", err)
	}
	if err := encoder.codecContext.Open(codec, codecOpts); err != nil {
		codecOpts.Free()
		encoder.close()
		return nil, fmt.Errorf("open libx264 encoder: %w", err)
	}
	codecOpts.Free()
	encoder.stream = encoder.formatContext.NewStream(nil)
	if encoder.stream == nil {
		encoder.close()
		return nil, fmt.Errorf("create MP4 video stream")
	}
	if err := encoder.stream.CodecParameters().FromCodecContext(encoder.codecContext); err != nil {
		encoder.close()
		return nil, fmt.Errorf("copy encoder parameters: %w", err)
	}
	encoder.stream.SetTimeBase(encoder.codecContext.TimeBase())
	encoder.ioContext, err = astiav.OpenIOContext(path, astiav.NewIOContextFlags(astiav.IOContextFlagWrite), nil, nil)
	if err != nil {
		encoder.close()
		return nil, fmt.Errorf("open MP4 output: %w", err)
	}
	encoder.formatContext.SetPb(encoder.ioContext)
	headerOpts := astiav.NewDictionary()
	if err := headerOpts.Set("movflags", "+faststart", astiav.NewDictionaryFlags()); err != nil {
		headerOpts.Free()
		encoder.close()
		return nil, fmt.Errorf("set MP4 output options: %w", err)
	}
	if err := encoder.formatContext.WriteHeader(headerOpts); err != nil {
		headerOpts.Free()
		encoder.close()
		return nil, fmt.Errorf("write MP4 header: %w", err)
	}
	headerOpts.Free()
	encoder.headerWritten = true
	encoder.scaler, err = astiav.CreateSoftwareScaleContext(
		width, height, sourceFormat,
		width, height, astiav.PixelFormatYuv420P,
		astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear),
	)
	if err != nil {
		encoder.close()
		return nil, fmt.Errorf("create video scaler: %w", err)
	}
	encoder.frame = astiav.AllocFrame()
	encoder.packet = astiav.AllocPacket()
	if encoder.frame == nil || encoder.packet == nil {
		encoder.close()
		return nil, fmt.Errorf("allocate encoder frame/packet")
	}
	encoder.frame.SetWidth(width)
	encoder.frame.SetHeight(height)
	encoder.frame.SetPixelFormat(astiav.PixelFormatYuv420P)
	if err := encoder.frame.AllocBuffer(32); err != nil {
		encoder.close()
		return nil, fmt.Errorf("allocate encoder frame buffer: %w", err)
	}
	return encoder, nil
}

func (encoder *videoEncoder) write(source *astiav.Frame, pts int64) error {
	if err := encoder.frame.MakeWritable(); err != nil {
		return fmt.Errorf("make encoder frame writable: %w", err)
	}
	if err := encoder.scaler.ScaleFrame(source, encoder.frame); err != nil {
		return fmt.Errorf("convert capture frame: %w", err)
	}
	encoder.frame.SetPts(pts)
	encoder.frame.SetPictureType(astiav.PictureTypeNone)
	return encoder.sendFrame(encoder.frame)
}

func (encoder *videoEncoder) sendFrame(frame *astiav.Frame) error {
	if err := encoder.codecContext.SendFrame(frame); err != nil {
		return fmt.Errorf("send encoder frame: %w", err)
	}
	for {
		err := encoder.codecContext.ReceivePacket(encoder.packet)
		if errors.Is(err, astiav.ErrEagain) || errors.Is(err, astiav.ErrEof) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("receive encoded packet: %w", err)
		}
		encoder.packet.SetStreamIndex(encoder.stream.Index())
		encoder.packet.RescaleTs(encoder.codecContext.TimeBase(), encoder.stream.TimeBase())
		err = encoder.formatContext.WriteInterleavedFrame(encoder.packet)
		encoder.packet.Unref()
		if err != nil {
			return fmt.Errorf("write encoded packet: %w", err)
		}
	}
}

func (encoder *videoEncoder) flush() error {
	if encoder == nil || encoder.codecContext == nil {
		return nil
	}
	return encoder.sendFrame(nil)
}

func (encoder *videoEncoder) close() error {
	if encoder == nil {
		return nil
	}
	var result error
	if encoder.headerWritten && !encoder.trailerWritten && encoder.formatContext != nil {
		if err := encoder.formatContext.WriteTrailer(); err != nil {
			result = fmt.Errorf("write MP4 trailer: %w", err)
		} else {
			encoder.trailerWritten = true
		}
	}
	if encoder.packet != nil {
		encoder.packet.Free()
	}
	if encoder.frame != nil {
		encoder.frame.Free()
	}
	if encoder.scaler != nil {
		encoder.scaler.Free()
	}
	if encoder.codecContext != nil {
		encoder.codecContext.Free()
	}
	if encoder.ioContext != nil {
		if err := encoder.ioContext.Close(); result == nil && err != nil {
			result = err
		}
	}
	if encoder.formatContext != nil {
		encoder.formatContext.Free()
	}
	return result
}
