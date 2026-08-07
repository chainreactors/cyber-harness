//go:build record_ffmpeg && cgo && (windows || linux)

package record

import (
	"context"
	"errors"
	"fmt"
	"image"
	"strconv"
	"sync"

	"github.com/asticode/go-astiav"
)

type nativeCaptureTarget struct {
	format  string
	url     string
	options map[string]string
}

type ffmpegBackend struct{}

var registerDevicesOnce sync.Once

func newPlatformBackend() captureBackend {
	registerDevicesOnce.Do(func() {
		astiav.RegisterAllDevices()
		astiav.SetLogLevel(astiav.LogLevelWarning)
	})
	return &ffmpegBackend{}
}

func (b *ffmpegBackend) Resolve(ctx context.Context, req captureRequest) (resolvedTarget, error) {
	return resolvePlatformTarget(ctx, req)
}

func (b *ffmpegBackend) Screenshot(ctx context.Context, target resolvedTarget) (image.Image, error) {
	pipeline, err := openInputPipeline(ctx, target, defaultFPS)
	if err != nil {
		return nil, err
	}
	defer pipeline.close()
	var output image.Image
	err = pipeline.nextFrame(ctx, func(frame *astiav.Frame) error {
		sws, err := astiav.CreateSoftwareScaleContext(
			frame.Width(), frame.Height(), frame.PixelFormat(),
			frame.Width(), frame.Height(), astiav.PixelFormatRgba,
			astiav.NewSoftwareScaleContextFlags(astiav.SoftwareScaleContextFlagBilinear),
		)
		if err != nil {
			return fmt.Errorf("create screenshot scaler: %w", err)
		}
		defer sws.Free()
		dst := astiav.AllocFrame()
		if dst == nil {
			return fmt.Errorf("allocate screenshot frame")
		}
		defer dst.Free()
		if err := sws.ScaleFrame(frame, dst); err != nil {
			return fmt.Errorf("scale screenshot frame: %w", err)
		}
		img, err := dst.Data().GuessImageFormat()
		if err != nil {
			return fmt.Errorf("create screenshot image: %w", err)
		}
		if err := dst.Data().ToImage(img); err != nil {
			return fmt.Errorf("copy screenshot pixels: %w", err)
		}
		output = img
		return nil
	})
	if err != nil {
		return nil, err
	}
	return output, nil
}

func (b *ffmpegBackend) Record(ctx context.Context, target resolvedTarget, output string, fps int) (result mediaInfo, retErr error) {
	pipeline, err := openInputPipeline(ctx, target, fps)
	if err != nil {
		return result, err
	}
	defer pipeline.close()

	var encoder *videoEncoder
	defer func() {
		if encoder == nil {
			return
		}
		if err := encoder.flush(); retErr == nil && err != nil {
			retErr = err
		}
		if err := encoder.close(); retErr == nil && err != nil {
			retErr = err
		}
	}()

	process := func(frame *astiav.Frame) error {
		if encoder == nil {
			width, height := frame.Width()&^1, frame.Height()&^1
			if width <= 0 || height <= 0 {
				return fmt.Errorf("invalid capture dimensions %dx%d", frame.Width(), frame.Height())
			}
			encoder, err = openVideoEncoder(output, fps, width, height, frame.PixelFormat())
			if err != nil {
				return err
			}
			result.Width, result.Height = width, height
		}
		if frame.Width() < result.Width || frame.Height() < result.Height {
			return fmt.Errorf("capture target shrank from %dx%d to %dx%d", result.Width, result.Height, frame.Width(), frame.Height())
		}
		if err := encoder.write(frame, result.Frames); err != nil {
			return err
		}
		result.Frames++
		return nil
	}

	for {
		err := pipeline.nextFrame(ctx, process)
		if err == nil {
			continue
		}
		if ctx.Err() != nil || errors.Is(err, astiav.ErrExit) {
			break
		}
		if errors.Is(err, astiav.ErrEof) {
			if result.Frames == 0 {
				return result, fmt.Errorf("capture target ended before producing frames")
			}
			return result, fmt.Errorf("capture target closed or became unavailable")
		}
		return result, err
	}
	if result.Frames == 0 {
		return result, fmt.Errorf("recording stopped before producing frames")
	}
	return result, nil
}

type inputPipeline struct {
	formatContext *astiav.FormatContext
	decoder       *astiav.CodecContext
	stream        *astiav.Stream
	packet        *astiav.Packet
	frame         *astiav.Frame
	interrupter   *astiav.IOInterrupter
	wakeDone      chan struct{}
	watchStopped  chan struct{}
}

func openInputPipeline(ctx context.Context, target resolvedTarget, fps int) (*inputPipeline, error) {
	native, ok := target.Native.(nativeCaptureTarget)
	if !ok {
		return nil, fmt.Errorf("invalid native capture target")
	}
	inputFormat := astiav.FindInputFormat(native.format)
	if inputFormat == nil {
		return nil, fmt.Errorf("FFmpeg input device %q is unavailable", native.format)
	}
	p := &inputPipeline{wakeDone: make(chan struct{}), watchStopped: make(chan struct{})}
	p.formatContext = astiav.AllocFormatContext()
	if p.formatContext == nil {
		return nil, fmt.Errorf("allocate input format context")
	}
	p.interrupter = astiav.NewIOInterrupter()
	p.formatContext.SetIOInterrupter(p.interrupter)
	go func() {
		defer close(p.watchStopped)
		select {
		case <-ctx.Done():
			p.interrupter.Interrupt()
		case <-p.wakeDone:
		}
	}()
	opts := astiav.NewDictionary()
	defer opts.Free()
	if err := opts.Set("framerate", strconv.Itoa(fps), astiav.NewDictionaryFlags()); err != nil {
		p.close()
		return nil, fmt.Errorf("set capture framerate: %w", err)
	}
	if err := opts.Set("draw_mouse", "1", astiav.NewDictionaryFlags()); err != nil {
		p.close()
		return nil, fmt.Errorf("set mouse capture option: %w", err)
	}
	for key, value := range native.options {
		if err := opts.Set(key, value, astiav.NewDictionaryFlags()); err != nil {
			p.close()
			return nil, fmt.Errorf("set capture option %s: %w", key, err)
		}
	}
	if err := p.formatContext.OpenInput(native.url, inputFormat, opts); err != nil {
		p.close()
		return nil, fmt.Errorf("open %s capture input: %w", native.format, err)
	}
	for _, stream := range p.formatContext.Streams() {
		if stream.CodecParameters().MediaType() == astiav.MediaTypeVideo {
			p.stream = stream
			break
		}
	}
	if p.stream == nil {
		p.close()
		return nil, fmt.Errorf("capture input has no video stream")
	}
	codec := astiav.FindDecoder(p.stream.CodecParameters().CodecID())
	if codec == nil {
		p.close()
		return nil, fmt.Errorf("decoder for %s is unavailable", p.stream.CodecParameters().CodecID())
	}
	p.decoder = astiav.AllocCodecContext(codec)
	if p.decoder == nil {
		p.close()
		return nil, fmt.Errorf("allocate capture decoder")
	}
	if err := p.stream.CodecParameters().ToCodecContext(p.decoder); err != nil {
		p.close()
		return nil, fmt.Errorf("configure capture decoder: %w", err)
	}
	p.decoder.SetTimeBase(p.stream.TimeBase())
	p.decoder.SetFramerate(astiav.NewRational(fps, 1))
	if err := p.decoder.Open(codec, nil); err != nil {
		p.close()
		return nil, fmt.Errorf("open capture decoder: %w", err)
	}
	p.packet = astiav.AllocPacket()
	p.frame = astiav.AllocFrame()
	if p.packet == nil || p.frame == nil {
		p.close()
		return nil, fmt.Errorf("allocate capture packet/frame")
	}
	return p, nil
}

func (p *inputPipeline) nextFrame(ctx context.Context, consume func(*astiav.Frame) error) error {
	for {
		if err := ctx.Err(); err != nil {
			p.interrupter.Interrupt()
			return err
		}
		if err := p.formatContext.ReadFrame(p.packet); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if p.packet.StreamIndex() != p.stream.Index() {
			p.packet.Unref()
			continue
		}
		p.packet.RescaleTs(p.stream.TimeBase(), p.decoder.TimeBase())
		err := p.decoder.SendPacket(p.packet)
		p.packet.Unref()
		if err != nil {
			return fmt.Errorf("send capture packet: %w", err)
		}
		for {
			err = p.decoder.ReceiveFrame(p.frame)
			if errors.Is(err, astiav.ErrEagain) {
				break
			}
			if err != nil {
				return err
			}
			err = consume(p.frame)
			p.frame.Unref()
			return err
		}
	}
}

func (p *inputPipeline) close() {
	if p == nil {
		return
	}
	select {
	case <-p.wakeDone:
	default:
		close(p.wakeDone)
	}
	if p.watchStopped != nil {
		<-p.watchStopped
	}
	if p.frame != nil {
		p.frame.Free()
	}
	if p.packet != nil {
		p.packet.Free()
	}
	if p.decoder != nil {
		p.decoder.Free()
	}
	if p.formatContext != nil {
		p.formatContext.CloseInput()
		p.formatContext.Free()
	}
	if p.interrupter != nil {
		p.interrupter.Free()
	}
}
