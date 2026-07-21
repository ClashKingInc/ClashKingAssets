//go:build darwin && cgo
#import <Foundation/Foundation.h>
#import <AVFoundation/AVFoundation.h>
#import <CoreMedia/CoreMedia.h>
#import <CoreVideo/CoreVideo.h>
#import <VideoToolbox/VideoToolbox.h>
#include <math.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

struct sc_hevc_encoder {
    AVAssetWriter *writer;
    AVAssetWriterInput *input;
    AVAssetWriterInputPixelBufferAdaptor *adaptor;
    int width, height, require_hardware;
};

static int sc_fail(int kind, char **out, NSString *message) {
    if (out) *out = strdup(message.UTF8String ?: "unknown AVFoundation error");
    return kind;
}
static NSString *sc_error(NSError *error, NSString *fallback) {
    if (!error) return fallback;
    return [NSString stringWithFormat:@"%@ (%@ %ld)", error.localizedDescription ?: fallback, error.domain, (long)error.code];
}
static BOOL sc_unavailable_error(NSError *error) {
    if (![error.domain isEqualToString:AVFoundationErrorDomain]) return NO;
    return error.code == AVErrorEncoderNotFound || error.code == AVErrorEncoderTemporarilyUnavailable;
}
static BOOL sc_hardware_unavailable_error(NSError *error, int require_hardware) {
    if (sc_unavailable_error(error)) return YES;
    return require_hardware && [error.domain isEqualToString:AVFoundationErrorDomain] && error.code == AVErrorEncodeFailed;
}
static int sc_writer_error(struct sc_hevc_encoder *e, NSString *fallback, char **out) {
    NSError *error = e->writer.error;
    int kind = sc_hardware_unavailable_error(error, e->require_hardware) ? -1 : 0;
    return sc_fail(kind, out, sc_error(error, fallback));
}

struct sc_hevc_encoder *sc_hevc_encoder_create(const char *path, int width, int height, int quality, int require_hardware, int *error_kind, char **error_message) {
    @autoreleasepool { @try {
        if (error_kind) *error_kind = 0;
        if (@available(macOS 10.15, *)) {
            NSString *pathString = [NSString stringWithUTF8String:path];
            if (!pathString) { sc_fail(0, error_message, @"HEVC output path is not valid UTF-8"); return NULL; }
            NSError *writerError = nil;
            AVAssetWriter *writer = [[AVAssetWriter alloc] initWithURL:[NSURL fileURLWithPath:pathString] fileType:AVFileTypeQuickTimeMovie error:&writerError];
            if (!writer) { sc_fail(0, error_message, sc_error(writerError, @"could not create AVAssetWriter")); return NULL; }
            (void)quality;
            NSDictionary *compression = @{
                (NSString *)kVTCompressionPropertyKey_AllowFrameReordering: @NO,
            };
            NSMutableDictionary *settings = [@{
                AVVideoCodecKey: AVVideoCodecTypeHEVC,
                AVVideoWidthKey: @(width),
                AVVideoHeightKey: @(height),
                AVVideoCompressionPropertiesKey: compression,
            } mutableCopy];
            if (require_hardware) settings[AVVideoEncoderSpecificationKey] = @{
                (NSString *)kVTVideoEncoderSpecification_RequireHardwareAcceleratedVideoEncoder: @YES,
            };
            if (![writer canApplyOutputSettings:settings forMediaType:AVMediaTypeVideo]) {
                [settings release]; [writer release]; if (error_kind) *error_kind = -1;
                sc_fail(-1, error_message, require_hardware ? @"hardware HEVC settings are unsupported" : @"HEVC settings are unsupported");
                return NULL;
            }
            AVAssetWriterInput *input = [[AVAssetWriterInput alloc] initWithMediaType:AVMediaTypeVideo outputSettings:settings];
            [settings release];
            if (!input || ![writer canAddInput:input]) {
                [input release]; [writer release]; if (error_kind) *error_kind = -1;
                sc_fail(-1, error_message, @"could not create an HEVC writer input"); return NULL;
            }
            input.expectsMediaDataInRealTime = NO;
            input.mediaTimeScale = 1000;
            [writer addInput:input];

            NSDictionary *attrs = @{
                (NSString *)kCVPixelBufferPixelFormatTypeKey: @(kCVPixelFormatType_32BGRA),
                (NSString *)kCVPixelBufferWidthKey: @(width),
                (NSString *)kCVPixelBufferHeightKey: @(height),
                (NSString *)kCVPixelBufferIOSurfacePropertiesKey: @{},
            };
            AVAssetWriterInputPixelBufferAdaptor *adaptor = [[AVAssetWriterInputPixelBufferAdaptor alloc] initWithAssetWriterInput:input sourcePixelBufferAttributes:attrs];
            if (!adaptor) { [input release]; [writer release]; sc_fail(0, error_message, @"could not create BGRA adaptor"); return NULL; }
            if (![writer startWriting]) {
                NSError *error = writer.error; int kind = sc_hardware_unavailable_error(error, require_hardware) ? -1 : 0;
                [adaptor release]; [input release]; [writer release]; if (error_kind) *error_kind = kind;
                sc_fail(kind, error_message, sc_error(error, @"AVAssetWriter could not start")); return NULL;
            }
            [writer startSessionAtSourceTime:kCMTimeZero];
            struct sc_hevc_encoder *e = calloc(1, sizeof(struct sc_hevc_encoder));
            if (!e) {
                [writer cancelWriting]; [adaptor release]; [input release]; [writer release];
                sc_fail(0, error_message, @"could not allocate native HEVC encoder"); return NULL;
            }
            e->writer = writer; e->input = input; e->adaptor = adaptor;
            e->width = width; e->height = height; e->require_hardware = require_hardware;
            return e;
        }
        if (error_kind) *error_kind = -1;
        sc_fail(-1, error_message, @"HEVC requires macOS 10.13 or later"); return NULL;
    } @catch (NSException *exception) {
        sc_fail(0, error_message, exception.reason ?: @"AVFoundation exception creating HEVC encoder"); return NULL;
    }}
}

int sc_hevc_encoder_add_frame(struct sc_hevc_encoder *e, const uint8_t *nrgba, int stride, int64_t pts_ms, char **error_message) {
    @autoreleasepool { @try {
        if (!e || !nrgba) return sc_fail(0, error_message, @"native HEVC encoder or frame is nil");
        NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:60];
        while (!e->input.readyForMoreMediaData) {
            if (e->writer.status == AVAssetWriterStatusFailed || e->writer.status == AVAssetWriterStatusCancelled)
                return sc_writer_error(e, @"AVAssetWriter stopped while waiting", error_message);
            if (deadline.timeIntervalSinceNow <= 0) return sc_fail(0, error_message, @"timed out waiting for AVAssetWriter");
            [NSThread sleepForTimeInterval:0.001];
        }
        CVPixelBufferRef buffer = NULL;
        CVReturn cvResult = CVPixelBufferPoolCreatePixelBuffer(kCFAllocatorDefault, e->adaptor.pixelBufferPool, &buffer);
        if (cvResult != kCVReturnSuccess || !buffer)
            return sc_fail(0, error_message, [NSString stringWithFormat:@"could not allocate BGRA pixel buffer (CoreVideo %d)", cvResult]);
        CVPixelBufferLockBaseAddress(buffer, 0);
        uint8_t *destination = CVPixelBufferGetBaseAddress(buffer);
        size_t destinationStride = CVPixelBufferGetBytesPerRow(buffer);
        for (int y = 0; y < e->height; y++) {
            const uint8_t *src = nrgba + (size_t)y * stride;
            uint8_t *dst = destination + (size_t)y * destinationStride;
            for (int x = 0; x < e->width; x++) {
                dst[x*4] = src[x*4+2];
                dst[x*4+1] = src[x*4+1];
                dst[x*4+2] = src[x*4];
                dst[x*4+3] = src[x*4+3];
            }
        }
        CVPixelBufferUnlockBaseAddress(buffer, 0);
        BOOL appended = [e->adaptor appendPixelBuffer:buffer withPresentationTime:CMTimeMake(pts_ms, 1000)];
        CVPixelBufferRelease(buffer);
        if (!appended) return sc_writer_error(e, @"AVAssetWriter rejected BGRA frame", error_message);
        return 1;
    } @catch (NSException *exception) {
        return sc_fail(0, error_message, exception.reason ?: @"AVFoundation exception appending HEVC frame");
    }}
}

int sc_hevc_encoder_finish(struct sc_hevc_encoder *e, int64_t duration_ms, char **error_message) {
    @autoreleasepool { @try {
        if (!e) return sc_fail(0, error_message, @"native HEVC encoder is nil");
        [e->input markAsFinished];
        [e->writer endSessionAtSourceTime:CMTimeMake(duration_ms, 1000)];
        dispatch_semaphore_t done = dispatch_semaphore_create(0);
        [e->writer finishWritingWithCompletionHandler:^{ dispatch_semaphore_signal(done); }];
        long waitResult = dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 60 * NSEC_PER_SEC));
        if (waitResult != 0) {
            [e->writer cancelWriting];
            return sc_fail(0, error_message, @"timed out after 60 seconds finalizing HEVC movie");
        }
        if (e->writer.status != AVAssetWriterStatusCompleted)
            return sc_writer_error(e, @"AVAssetWriter did not complete HEVC movie", error_message);
        return 1;
    } @catch (NSException *exception) {
        return sc_fail(0, error_message, exception.reason ?: @"AVFoundation exception finishing HEVC movie");
    }}
}

void sc_hevc_encoder_abort(struct sc_hevc_encoder *e) {
    @autoreleasepool { if (e && e->writer.status != AVAssetWriterStatusCompleted) [e->writer cancelWriting]; }
}
void sc_hevc_encoder_destroy(struct sc_hevc_encoder *e) {
    if (!e) return;
    [e->adaptor release]; [e->input release]; [e->writer release]; free(e);
}

int sc_hevc_inspect(const char *path, int *frame_count, int64_t *duration_ms, uint32_t *codec, int *straight_alpha, int *minimum_alpha, int *maximum_alpha, char **error_message) {
    @autoreleasepool { @try {
        NSString *pathString = [NSString stringWithUTF8String:path];
        AVURLAsset *asset = [AVURLAsset URLAssetWithURL:[NSURL fileURLWithPath:pathString] options:@{AVURLAssetPreferPreciseDurationAndTimingKey:@YES}];
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
        NSArray<AVAssetTrack *> *tracks = [asset tracksWithMediaType:AVMediaTypeVideo];
#pragma clang diagnostic pop
        if (tracks.count != 1) return sc_fail(0, error_message, [NSString stringWithFormat:@"HEVC movie has %lu video tracks", (unsigned long)tracks.count]);
        AVAssetTrack *track = tracks.firstObject;
        if (!track.formatDescriptions.count) return sc_fail(0, error_message, @"HEVC track has no format description");
        CMFormatDescriptionRef format = (CMFormatDescriptionRef)track.formatDescriptions.firstObject;
        if (codec) *codec = CMFormatDescriptionGetMediaSubType(format);
        CFTypeRef alphaMode = CMFormatDescriptionGetExtension(format, kCMFormatDescriptionExtension_AlphaChannelMode);
        if (straight_alpha) *straight_alpha = alphaMode && CFEqual(alphaMode, kCMFormatDescriptionAlphaChannelMode_StraightAlpha);
        NSError *readerError = nil;
        AVAssetReader *reader = [[AVAssetReader alloc] initWithAsset:asset error:&readerError];
        if (!reader) return sc_fail(0, error_message, sc_error(readerError, @"could not create asset reader"));
        AVAssetReaderTrackOutput *output = [[AVAssetReaderTrackOutput alloc] initWithTrack:track outputSettings:@{(NSString *)kCVPixelBufferPixelFormatTypeKey:@(kCVPixelFormatType_32BGRA)}];
        output.alwaysCopiesSampleData = NO;
        if (![reader canAddOutput:output]) { [output release]; [reader release]; return sc_fail(0, error_message, @"could not add asset reader output"); }
        [reader addOutput:output];
        if (![reader startReading]) {
            NSError *error = reader.error; [output release]; [reader release];
            return sc_fail(0, error_message, sc_error(error, @"could not start asset reader"));
        }
        int frames = 0, minAlpha = 255, maxAlpha = 0;
        CMSampleBufferRef sample;
        while ((sample = [output copyNextSampleBuffer])) {
            CVPixelBufferRef buffer = CMSampleBufferGetImageBuffer(sample);
            if (buffer) {
                CVPixelBufferLockBaseAddress(buffer, kCVPixelBufferLock_ReadOnly);
                const uint8_t *base = CVPixelBufferGetBaseAddress(buffer);
                size_t rowBytes = CVPixelBufferGetBytesPerRow(buffer), width = CVPixelBufferGetWidth(buffer), height = CVPixelBufferGetHeight(buffer);
                for (size_t y = 0; y < height; y++) for (size_t x = 0; x < width; x++) {
                    int alpha = base[y*rowBytes+x*4+3];
                    if (alpha < minAlpha) minAlpha = alpha;
                    if (alpha > maxAlpha) maxAlpha = alpha;
                }
                CVPixelBufferUnlockBaseAddress(buffer, kCVPixelBufferLock_ReadOnly);
            }
            frames++; CFRelease(sample);
        }
        AVAssetReaderStatus status = reader.status;
        NSError *error = reader.error;
        [output release]; [reader release];
        if (status != AVAssetReaderStatusCompleted) return sc_fail(0, error_message, sc_error(error, @"asset reader did not complete"));
        if (frame_count) *frame_count = frames;
        if (duration_ms) *duration_ms = (int64_t)llround(CMTimeGetSeconds(asset.duration) * 1000.0);
        if (minimum_alpha) *minimum_alpha = minAlpha;
        if (maximum_alpha) *maximum_alpha = maxAlpha;
        return 1;
    } @catch (NSException *exception) {
        return sc_fail(0, error_message, exception.reason ?: @"AVFoundation exception inspecting HEVC movie");
    }}
}
