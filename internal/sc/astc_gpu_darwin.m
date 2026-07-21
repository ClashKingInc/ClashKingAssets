//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <Metal/Metal.h>
#include <stdlib.h>
#include <string.h>

static id<MTLDevice> sc_device;
static id<MTLCommandQueue> sc_command_queue;
static id<MTLRenderPipelineState> sc_linear_pipeline;
static id<MTLRenderPipelineState> sc_srgb_pipeline;
static char *sc_initialization_error;

static int sc_fail(char **error_message, NSString *message) {
    if (error_message != NULL) {
        const char *utf8 = message.UTF8String;
        *error_message = strdup(utf8 != NULL ? utf8 : "unknown Metal error");
    }
    return 0;
}

static void sc_initialize_metal(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        sc_device = MTLCreateSystemDefaultDevice();
        if (sc_device == nil) {
            sc_initialization_error = strdup("no Metal device is available");
            return;
        }
        sc_command_queue = [sc_device newCommandQueue];
        if (sc_command_queue == nil) {
            sc_initialization_error = strdup("could not create a Metal command queue");
            return;
        }

        NSString *source =
            @"#include <metal_stdlib>\n"
             "using namespace metal;\n"
             "struct RasterData { float4 position [[position]]; };\n"
             "vertex RasterData sc_vertex(uint vertex_id [[vertex_id]]) {\n"
             "  float2 positions[3] = {float2(-1.0, -1.0), float2(3.0, -1.0), float2(-1.0, 3.0)};\n"
             "  RasterData out; out.position = float4(positions[vertex_id], 0.0, 1.0); return out;\n"
             "}\n"
             "fragment float4 sc_fragment(RasterData in [[stage_in]], texture2d<float> source [[texture(0)]]) {\n"
             "  return source.read(uint2(in.position.xy));\n"
             "}\n";
        NSError *library_error = nil;
        id<MTLLibrary> library = [sc_device newLibraryWithSource:source options:nil error:&library_error];
        if (library == nil) {
            sc_initialization_error = strdup(library_error.localizedDescription.UTF8String ?: "could not compile Metal ASTC shaders");
            return;
        }
        id<MTLFunction> vertex = [library newFunctionWithName:@"sc_vertex"];
        id<MTLFunction> fragment = [library newFunctionWithName:@"sc_fragment"];

        MTLRenderPipelineDescriptor *descriptor = [[MTLRenderPipelineDescriptor alloc] init];
        descriptor.vertexFunction = vertex;
        descriptor.fragmentFunction = fragment;
        descriptor.colorAttachments[0].pixelFormat = MTLPixelFormatRGBA8Unorm;
        NSError *pipeline_error = nil;
        sc_linear_pipeline = [sc_device newRenderPipelineStateWithDescriptor:descriptor error:&pipeline_error];
        if (sc_linear_pipeline == nil) {
            sc_initialization_error = strdup(pipeline_error.localizedDescription.UTF8String ?: "could not create the linear Metal ASTC pipeline");
        } else {
            descriptor.colorAttachments[0].pixelFormat = MTLPixelFormatRGBA8Unorm_sRGB;
            sc_srgb_pipeline = [sc_device newRenderPipelineStateWithDescriptor:descriptor error:&pipeline_error];
            if (sc_srgb_pipeline == nil) {
                sc_initialization_error = strdup(pipeline_error.localizedDescription.UTF8String ?: "could not create the sRGB Metal ASTC pipeline");
            }
        }

        [descriptor release];
        [fragment release];
        [vertex release];
        [library release];
    });
}

static MTLPixelFormat sc_astc_pixel_format(int block_x, int block_y, int srgb) {
    if (srgb) {
        if (block_x == 4 && block_y == 4) return MTLPixelFormatASTC_4x4_sRGB;
        if (block_x == 5 && block_y == 4) return MTLPixelFormatASTC_5x4_sRGB;
        if (block_x == 5 && block_y == 5) return MTLPixelFormatASTC_5x5_sRGB;
        if (block_x == 6 && block_y == 5) return MTLPixelFormatASTC_6x5_sRGB;
        if (block_x == 6 && block_y == 6) return MTLPixelFormatASTC_6x6_sRGB;
        if (block_x == 8 && block_y == 5) return MTLPixelFormatASTC_8x5_sRGB;
        if (block_x == 8 && block_y == 6) return MTLPixelFormatASTC_8x6_sRGB;
        if (block_x == 8 && block_y == 8) return MTLPixelFormatASTC_8x8_sRGB;
        if (block_x == 10 && block_y == 5) return MTLPixelFormatASTC_10x5_sRGB;
        if (block_x == 10 && block_y == 6) return MTLPixelFormatASTC_10x6_sRGB;
        if (block_x == 10 && block_y == 8) return MTLPixelFormatASTC_10x8_sRGB;
        if (block_x == 10 && block_y == 10) return MTLPixelFormatASTC_10x10_sRGB;
        if (block_x == 12 && block_y == 10) return MTLPixelFormatASTC_12x10_sRGB;
        if (block_x == 12 && block_y == 12) return MTLPixelFormatASTC_12x12_sRGB;
    } else {
        if (block_x == 4 && block_y == 4) return MTLPixelFormatASTC_4x4_LDR;
        if (block_x == 5 && block_y == 4) return MTLPixelFormatASTC_5x4_LDR;
        if (block_x == 5 && block_y == 5) return MTLPixelFormatASTC_5x5_LDR;
        if (block_x == 6 && block_y == 5) return MTLPixelFormatASTC_6x5_LDR;
        if (block_x == 6 && block_y == 6) return MTLPixelFormatASTC_6x6_LDR;
        if (block_x == 8 && block_y == 5) return MTLPixelFormatASTC_8x5_LDR;
        if (block_x == 8 && block_y == 6) return MTLPixelFormatASTC_8x6_LDR;
        if (block_x == 8 && block_y == 8) return MTLPixelFormatASTC_8x8_LDR;
        if (block_x == 10 && block_y == 5) return MTLPixelFormatASTC_10x5_LDR;
        if (block_x == 10 && block_y == 6) return MTLPixelFormatASTC_10x6_LDR;
        if (block_x == 10 && block_y == 8) return MTLPixelFormatASTC_10x8_LDR;
        if (block_x == 10 && block_y == 10) return MTLPixelFormatASTC_10x10_LDR;
        if (block_x == 12 && block_y == 10) return MTLPixelFormatASTC_12x10_LDR;
        if (block_x == 12 && block_y == 12) return MTLPixelFormatASTC_12x12_LDR;
    }
    return MTLPixelFormatInvalid;
}

int sc_decode_astc_metal(
    const uint8_t *src,
    size_t src_len,
    uint8_t *dst,
    int width,
    int height,
    int block_x,
    int block_y,
    int srgb,
    char **error_message
) {
    @autoreleasepool {
        @try {
          if (@available(macOS 11.0, *)) {
            sc_initialize_metal();
            if (sc_initialization_error != NULL) {
                return sc_fail(error_message, [NSString stringWithUTF8String:sc_initialization_error]);
            }

            MTLPixelFormat source_format = sc_astc_pixel_format(block_x, block_y, srgb);
            if (source_format == MTLPixelFormatInvalid) {
                return sc_fail(error_message, @"unsupported ASTC block size");
            }
            NSUInteger blocks_wide = ((NSUInteger)width + (NSUInteger)block_x - 1) / (NSUInteger)block_x;
            NSUInteger blocks_high = ((NSUInteger)height + (NSUInteger)block_y - 1) / (NSUInteger)block_y;
            NSUInteger source_bytes_per_row = blocks_wide * 16;
            if (src_len < source_bytes_per_row * blocks_high) {
                return sc_fail(error_message, @"short ASTC payload");
            }

            MTLTextureDescriptor *source_descriptor = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:source_format width:(NSUInteger)width height:(NSUInteger)height mipmapped:NO];
            source_descriptor.storageMode = MTLStorageModeShared;
            source_descriptor.usage = MTLTextureUsageShaderRead;
            id<MTLTexture> source_texture = [sc_device newTextureWithDescriptor:source_descriptor];
            if (source_texture == nil) {
                return sc_fail(error_message, @"could not create the Metal ASTC texture");
            }
            [source_texture replaceRegion:MTLRegionMake2D(0, 0, width, height) mipmapLevel:0 withBytes:src bytesPerRow:source_bytes_per_row];

            MTLPixelFormat target_format = srgb ? MTLPixelFormatRGBA8Unorm_sRGB : MTLPixelFormatRGBA8Unorm;
            MTLTextureDescriptor *target_descriptor = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:target_format width:(NSUInteger)width height:(NSUInteger)height mipmapped:NO];
            target_descriptor.storageMode = MTLStorageModePrivate;
            target_descriptor.usage = MTLTextureUsageRenderTarget;
            id<MTLTexture> target_texture = [sc_device newTextureWithDescriptor:target_descriptor];
            if (target_texture == nil) {
                [source_texture release];
                return sc_fail(error_message, @"could not create the Metal RGBA texture");
            }

            NSUInteger output_bytes_per_row = (((NSUInteger)width * 4) + 255) & ~(NSUInteger)255;
            id<MTLBuffer> output_buffer = [sc_device newBufferWithLength:output_bytes_per_row * (NSUInteger)height options:MTLResourceStorageModeShared];
            if (output_buffer == nil) {
                [target_texture release];
                [source_texture release];
                return sc_fail(error_message, @"could not create the Metal readback buffer");
            }
            id<MTLCommandBuffer> command_buffer = [sc_command_queue commandBuffer];
            if (command_buffer == nil) {
                [output_buffer release];
                [target_texture release];
                [source_texture release];
                return sc_fail(error_message, @"could not create a Metal command buffer");
            }

            MTLRenderPassDescriptor *pass = [MTLRenderPassDescriptor renderPassDescriptor];
            pass.colorAttachments[0].texture = target_texture;
            pass.colorAttachments[0].loadAction = MTLLoadActionDontCare;
            pass.colorAttachments[0].storeAction = MTLStoreActionStore;
            id<MTLRenderCommandEncoder> render = [command_buffer renderCommandEncoderWithDescriptor:pass];
            if (render == nil) {
                [output_buffer release];
                [target_texture release];
                [source_texture release];
                return sc_fail(error_message, @"could not create a Metal render encoder");
            }
            [render setRenderPipelineState:srgb ? sc_srgb_pipeline : sc_linear_pipeline];
            [render setFragmentTexture:source_texture atIndex:0];
            [render drawPrimitives:MTLPrimitiveTypeTriangle vertexStart:0 vertexCount:3];
            [render endEncoding];

            id<MTLBlitCommandEncoder> blit = [command_buffer blitCommandEncoder];
            if (blit == nil) {
                [output_buffer release];
                [target_texture release];
                [source_texture release];
                return sc_fail(error_message, @"could not create a Metal readback encoder");
            }
            [blit copyFromTexture:target_texture
                      sourceSlice:0
                      sourceLevel:0
                     sourceOrigin:MTLOriginMake(0, 0, 0)
                       sourceSize:MTLSizeMake(width, height, 1)
                         toBuffer:output_buffer
                destinationOffset:0
           destinationBytesPerRow:output_bytes_per_row
         destinationBytesPerImage:output_bytes_per_row * (NSUInteger)height];
            [blit endEncoding];
            [command_buffer commit];
            [command_buffer waitUntilCompleted];

            int result = 1;
            if (command_buffer.status == MTLCommandBufferStatusError) {
                result = sc_fail(error_message, command_buffer.error.localizedDescription ?: @"Metal ASTC command failed");
            } else {
                const uint8_t *output = (const uint8_t *)output_buffer.contents;
                for (int y = 0; y < height; y++) {
                    memcpy(dst + (size_t)y * (size_t)width * 4, output + (size_t)y * output_bytes_per_row, (size_t)width * 4);
                }
            }

            [output_buffer release];
            [target_texture release];
            [source_texture release];
            return result;
          }
          return sc_fail(error_message, @"Metal ASTC decoding requires macOS 11 or later");
        } @catch (NSException *exception) {
            return sc_fail(error_message, exception.reason ?: @"Metal raised an exception while decoding ASTC");
        }
    }
}
