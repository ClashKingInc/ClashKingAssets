//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <Metal/Metal.h>
#include <stdlib.h>
#include <string.h>

typedef struct sc_metal_compositor {
    id<MTLCommandQueue> queue;
    id<MTLTexture> target;
    id<MTLBuffer> readback;
    id<MTLCommandBuffer> command_buffer;
    NSUInteger readback_bytes_per_row;
    int width;
    int height;
} sc_metal_compositor;

typedef void sc_metal_texture;

typedef struct sc_clear_uniforms {
    float red;
    float green;
    float blue;
    float alpha;
    uint32_t width;
    uint32_t height;
} sc_clear_uniforms;

typedef struct sc_draw_uniforms {
    float inverse_a;
    float inverse_b;
    float inverse_c;
    float inverse_d;
    float inverse_tx;
    float inverse_ty;
    float red_add;
    float green_add;
    float blue_add;
    float alpha_mul;
    float red_mul;
    float green_mul;
    float blue_mul;
    uint32_t blend_mode;
    uint32_t allow_additive_coverage;
    uint32_t luminance_floor;
    uint32_t nrgba_over_fast_path;
    int32_t left;
    int32_t top;
    uint32_t width;
    uint32_t height;
    uint32_t sprite_width;
    uint32_t sprite_height;
    uint32_t has_mask;
} sc_draw_uniforms;

typedef struct sc_mask_uniforms {
    uint32_t width;
    uint32_t height;
} sc_mask_uniforms;

static id<MTLDevice> sc_compositor_device;
static id<MTLComputePipelineState> sc_clear_pipeline;
static id<MTLComputePipelineState> sc_draw_pipeline;
static id<MTLComputePipelineState> sc_mask_pipeline;
static char *sc_compositor_initialization_error;

void sc_metal_compositor_destroy(sc_metal_compositor *compositor);

static int sc_compositor_fail(char **error_message, NSString *message) {
    if (error_message != NULL) {
        const char *utf8 = message.UTF8String;
        *error_message = strdup(utf8 != NULL ? utf8 : "unknown Metal compositor error");
    }
    return 0;
}

static void sc_initialize_compositor_metal(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        sc_compositor_device = MTLCreateSystemDefaultDevice();
        if (sc_compositor_device == nil) {
            sc_compositor_initialization_error = strdup("no Metal device is available");
            return;
        }

        NSString *source =
            @"#include <metal_stdlib>\n"
             "using namespace metal;\n"
             "struct ClearUniforms { float red; float green; float blue; float alpha; uint width; uint height; };\n"
             "struct DrawUniforms {\n"
             "  float inverse_a; float inverse_b; float inverse_c; float inverse_d; float inverse_tx; float inverse_ty;\n"
             "  float red_add; float green_add; float blue_add; float alpha_mul; float red_mul; float green_mul; float blue_mul;\n"
             "  uint blend_mode; uint allow_additive_coverage; uint luminance_floor; uint nrgba_over_fast_path; int left; int top;\n"
             "  uint width; uint height; uint sprite_width; uint sprite_height; uint has_mask;\n"
             "};\n"
             "struct MaskUniforms { uint width; uint height; };\n"
             "float quantize(float value) { return floor(clamp(value, 0.0f, 1.0f) * 255.0f + 0.5f) / 255.0f; }\n"
             "float4 quantize4(float4 value) { return float4(quantize(value.r), quantize(value.g), quantize(value.b), quantize(value.a)); }\n"
             "uint byte_value(float value) { return uint(floor(clamp(value, 0.0f, 1.0f) * 255.0f + 0.5f)); }\n"
             "float transformed_byte(float value, float multiplier, float addition) {\n"
             "  return floor(clamp(value * 255.0f * multiplier + addition, 0.0f, 255.0f) + 0.5f) / 255.0f;\n"
             "}\n"
             "float4 premultiplied(float4 value) { return float4(value.rgb * value.a, value.a); }\n"
             "float4 sample_sprite(texture2d<float, access::read> sprite, float2 source_position, uint width, uint height) {\n"
             "  float2 sample_position = source_position - 0.5f;\n"
             "  int2 p0 = int2(floor(sample_position));\n"
             "  float2 fraction = sample_position - floor(sample_position);\n"
             "  int2 maximum = int2(int(width) - 1, int(height) - 1);\n"
             "  uint2 c00 = uint2(clamp(p0, int2(0), maximum));\n"
             "  uint2 c10 = uint2(clamp(p0 + int2(1, 0), int2(0), maximum));\n"
             "  uint2 c01 = uint2(clamp(p0 + int2(0, 1), int2(0), maximum));\n"
             "  uint2 c11 = uint2(clamp(p0 + int2(1, 1), int2(0), maximum));\n"
             "  float4 top = mix(premultiplied(sprite.read(c00)), premultiplied(sprite.read(c10)), fraction.x);\n"
             "  float4 bottom = mix(premultiplied(sprite.read(c01)), premultiplied(sprite.read(c11)), fraction.x);\n"
             "  float4 sample = mix(top, bottom, fraction.y);\n"
             "  if (sample.a <= 0.0f) return float4(0.0f);\n"
             "  return quantize4(float4(sample.rgb / sample.a, sample.a));\n"
             "}\n"
             "float4 transform_color(float4 source, constant DrawUniforms &u) {\n"
             "  return float4(\n"
             "    transformed_byte(source.r, u.red_mul, u.red_add),\n"
             "    transformed_byte(source.g, u.green_mul, u.green_add),\n"
             "    transformed_byte(source.b, u.blue_mul, u.blue_add),\n"
             "    transformed_byte(source.a, u.alpha_mul, 0.0f));\n"
             "}\n"
             "float4 compose_over(float4 destination, float4 source) {\n"
             "  float out_alpha = source.a + destination.a * (1.0f - source.a);\n"
             "  if (out_alpha <= 0.0f) return float4(0.0f);\n"
             "  float3 out_color = (source.rgb * source.a + destination.rgb * destination.a * (1.0f - source.a)) / out_alpha;\n"
             "  return quantize4(float4(out_color, out_alpha));\n"
             "}\n"
             "float4 compose_nrgba_over_fast(float4 destination, float4 source) {\n"
             "  uint4 source_byte = uint4(byte_value(source.r), byte_value(source.g), byte_value(source.b), byte_value(source.a));\n"
             "  uint4 destination_byte = uint4(byte_value(destination.r), byte_value(destination.g), byte_value(destination.b), byte_value(destination.a));\n"
             "  uint source_alpha_16 = source_byte.a * 257u;\n"
             "  uint4 source_premultiplied = uint4(\n"
             "    (source_byte.r * 257u * source_byte.a) / 255u,\n"
             "    (source_byte.g * 257u * source_byte.a) / 255u,\n"
             "    (source_byte.b * 257u * source_byte.a) / 255u, source_alpha_16);\n"
             "  uint4 destination_premultiplied = uint4(\n"
             "    (destination_byte.r * 257u * destination_byte.a) / 255u,\n"
             "    (destination_byte.g * 257u * destination_byte.a) / 255u,\n"
             "    (destination_byte.b * 257u * destination_byte.a) / 255u, destination_byte.a * 257u);\n"
             "  uint inverse_alpha = 65535u - source_alpha_16;\n"
             "  uint4 output_premultiplied = destination_premultiplied * inverse_alpha / 65535u + source_premultiplied;\n"
             "  uint output_alpha = output_premultiplied.a;\n"
             "  uint3 output_color = output_premultiplied.rgb;\n"
             "  if (output_alpha != 0u && output_alpha != 65535u) output_color = output_color * 65535u / output_alpha;\n"
             "  return float4(float3(output_color >> 8u), float(output_alpha >> 8u)) / 255.0f;\n"
             "}\n"
             "float4 compose_add(float4 destination, float4 source, bool allow_coverage) {\n"
             "  if (source.a <= 0.0f || (destination.a <= 0.0f && !allow_coverage)) return destination;\n"
             "  uint maximum = max(byte_value(source.r), max(byte_value(source.g), byte_value(source.b)));\n"
             "  if (maximum <= 24u) return destination;\n"
             "  float light_alpha = min(1.0f, float(maximum - 24u) / 64.0f);\n"
             "  uint source_alpha_byte = uint(floor(float(byte_value(source.a)) * light_alpha + 0.5f));\n"
             "  if (source_alpha_byte == 0u) return destination;\n"
             "  float source_alpha = float(source_alpha_byte) / 255.0f;\n"
             "  float out_alpha = source_alpha + destination.a * (1.0f - source_alpha);\n"
             "  float3 premultiplied_color = destination.rgb * destination.a + source.rgb * source_alpha;\n"
             "  float3 out_color = min(premultiplied_color, float3(out_alpha)) / out_alpha;\n"
             "  return quantize4(float4(out_color, out_alpha));\n"
             "}\n"
             "float4 compose_screen(float4 destination, float4 source) {\n"
             "  float intensity = float(max(byte_value(source.r), max(byte_value(source.g), byte_value(source.b)))) / 255.0f;\n"
             "  float source_alpha = source.a * intensity;\n"
             "  if (source_alpha <= 0.0f) return destination;\n"
             "  float out_alpha = source_alpha + destination.a * (1.0f - source_alpha);\n"
             "  if (out_alpha <= 0.0f) return destination;\n"
             "  float3 screen_color = 1.0f - (1.0f - destination.rgb) * (1.0f - source.rgb);\n"
             "  float3 premultiplied_color = (1.0f - source_alpha) * destination.rgb * destination.a\n"
             "    + (1.0f - destination.a) * source.rgb * source_alpha\n"
             "    + source_alpha * destination.a * screen_color;\n"
             "  return quantize4(float4(premultiplied_color / out_alpha, out_alpha));\n"
             "}\n"
             "float4 compose_multiply(float4 destination, float4 source) {\n"
             "  if (destination.a <= 0.0f || source.a <= 0.0f) return destination;\n"
             "  float3 multiplied = destination.rgb * source.rgb;\n"
             "  return quantize4(float4(destination.rgb * (1.0f - source.a) + multiplied * source.a, destination.a));\n"
             "}\n"
             "kernel void clear_canvas(texture2d<float, access::write> target [[texture(0)]],\n"
             "                         constant ClearUniforms &u [[buffer(0)]], uint2 gid [[thread_position_in_grid]]) {\n"
             "  if (gid.x >= u.width || gid.y >= u.height) return;\n"
             "  target.write(float4(u.red, u.green, u.blue, u.alpha), gid);\n"
             "}\n"
             "kernel void composite_sprite(texture2d<float, access::read> sprite [[texture(0)]],\n"
             "                             texture2d<float, access::read_write> target [[texture(1)]],\n"
             "                             texture2d<float, access::read> alpha_mask [[texture(2)]],\n"
             "                             constant DrawUniforms &u [[buffer(0)]], uint2 gid [[thread_position_in_grid]]) {\n"
             "  if (gid.x >= u.width || gid.y >= u.height) return;\n"
             "  int2 destination_position = int2(u.left, u.top) + int2(gid);\n"
             "  float x = float(destination_position.x) + 0.5f;\n"
             "  float y = float(destination_position.y) + 0.5f;\n"
             "  float2 source_position = float2(\n"
             "    u.inverse_a * x + u.inverse_c * y + u.inverse_tx,\n"
             "    u.inverse_b * x + u.inverse_d * y + u.inverse_ty);\n"
             "  if (source_position.x < 0.0f || source_position.y < 0.0f\n"
             "      || source_position.x >= float(u.sprite_width) || source_position.y >= float(u.sprite_height)) return;\n"
             "  float4 source = sample_sprite(sprite, source_position, u.sprite_width, u.sprite_height);\n"
             "  if (source.a <= 0.0f) return;\n"
             "  if (u.has_mask != 0u) {\n"
             "    uint masked_alpha = byte_value(source.a) * byte_value(alpha_mask.read(uint2(destination_position)).a) / 255u;\n"
             "    source.a = float(masked_alpha) / 255.0f;\n"
             "    if (masked_alpha == 0u) return;\n"
             "  }\n"
             "  if (u.blend_mode == 1u || u.blend_mode == 2u) {\n"
             "    uint maximum = max(byte_value(source.r), max(byte_value(source.g), byte_value(source.b)));\n"
             "    uint coverage = maximum > u.luminance_floor ? maximum - u.luminance_floor : 0u;\n"
             "    uint adjusted_alpha = byte_value(source.a) * coverage / 255u;\n"
             "    source.a = float(adjusted_alpha) / 255.0f;\n"
             "  }\n"
             "  source = transform_color(source, u);\n"
             "  if (source.a <= 0.0f) return;\n"
             "  uint2 destination_coordinate = uint2(destination_position);\n"
             "  float4 destination = target.read(destination_coordinate);\n"
             "  float4 result;\n"
             "  if (u.blend_mode == 1u) result = compose_add(destination, source, u.allow_additive_coverage != 0u);\n"
             "  else if (u.blend_mode == 2u) result = compose_screen(destination, source);\n"
             "  else if (u.blend_mode == 3u) result = compose_multiply(destination, source);\n"
             "  else if (u.nrgba_over_fast_path != 0u) result = compose_nrgba_over_fast(destination, source);\n"
             "  else result = compose_over(destination, source);\n"
             "  target.write(result, destination_coordinate);\n"
             "}\n"
             "kernel void combine_masks(texture2d<float, access::read> first [[texture(0)]],\n"
             "                          texture2d<float, access::read> second [[texture(1)]],\n"
             "                          texture2d<float, access::write> output [[texture(2)]],\n"
             "                          constant MaskUniforms &u [[buffer(0)]], uint2 gid [[thread_position_in_grid]]) {\n"
             "  if (gid.x >= u.width || gid.y >= u.height) return;\n"
             "  uint alpha = byte_value(first.read(gid).a) * byte_value(second.read(gid).a) / 255u;\n"
             "  output.write(float4(1.0f, 1.0f, 1.0f, float(alpha) / 255.0f), gid);\n"
             "}\n";

        NSError *library_error = nil;
        id<MTLLibrary> library = [sc_compositor_device newLibraryWithSource:source options:nil error:&library_error];
        if (library == nil) {
            sc_compositor_initialization_error = strdup(library_error.localizedDescription.UTF8String ?: "could not compile Metal compositor shaders");
            return;
        }
        id<MTLFunction> clear_function = [library newFunctionWithName:@"clear_canvas"];
        id<MTLFunction> draw_function = [library newFunctionWithName:@"composite_sprite"];
        id<MTLFunction> mask_function = [library newFunctionWithName:@"combine_masks"];
        NSError *pipeline_error = nil;
        sc_clear_pipeline = [sc_compositor_device newComputePipelineStateWithFunction:clear_function error:&pipeline_error];
        if (sc_clear_pipeline == nil) {
            sc_compositor_initialization_error = strdup(pipeline_error.localizedDescription.UTF8String ?: "could not create Metal clear pipeline");
        } else {
            sc_draw_pipeline = [sc_compositor_device newComputePipelineStateWithFunction:draw_function error:&pipeline_error];
            if (sc_draw_pipeline == nil) {
                sc_compositor_initialization_error = strdup(pipeline_error.localizedDescription.UTF8String ?: "could not create Metal compositor pipeline");
            } else {
                sc_mask_pipeline = [sc_compositor_device newComputePipelineStateWithFunction:mask_function error:&pipeline_error];
                if (sc_mask_pipeline == nil) {
                    sc_compositor_initialization_error = strdup(pipeline_error.localizedDescription.UTF8String ?: "could not create Metal mask pipeline");
                }
            }
        }
        [mask_function release];
        [draw_function release];
        [clear_function release];
        [library release];
    });
}

static MTLSize sc_threadgroup_size(id<MTLComputePipelineState> pipeline) {
    NSUInteger width = MIN((NSUInteger)16, pipeline.threadExecutionWidth);
    NSUInteger height = MIN((NSUInteger)16, pipeline.maxTotalThreadsPerThreadgroup / MAX(width, (NSUInteger)1));
    return MTLSizeMake(MAX(width, (NSUInteger)1), MAX(height, (NSUInteger)1), 1);
}

sc_metal_compositor *sc_metal_compositor_create(int width, int height, char **error_message) {
    @autoreleasepool {
        @try {
            sc_initialize_compositor_metal();
            if (sc_compositor_initialization_error != NULL) {
                sc_compositor_fail(error_message, [NSString stringWithUTF8String:sc_compositor_initialization_error]);
                return NULL;
            }
            sc_metal_compositor *compositor = calloc(1, sizeof(sc_metal_compositor));
            compositor->width = width;
            compositor->height = height;
            compositor->queue = [sc_compositor_device newCommandQueue];

            MTLTextureDescriptor *descriptor = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatRGBA8Unorm width:(NSUInteger)width height:(NSUInteger)height mipmapped:NO];
            descriptor.storageMode = MTLStorageModePrivate;
            descriptor.usage = MTLTextureUsageShaderRead | MTLTextureUsageShaderWrite;
            compositor->target = [sc_compositor_device newTextureWithDescriptor:descriptor];
            compositor->readback_bytes_per_row = (((NSUInteger)width * 4) + 255) & ~(NSUInteger)255;
            compositor->readback = [sc_compositor_device newBufferWithLength:compositor->readback_bytes_per_row * (NSUInteger)height options:MTLResourceStorageModeShared];
            if (compositor->queue == nil || compositor->target == nil || compositor->readback == nil) {
                sc_compositor_fail(error_message, @"could not allocate Metal compositor resources");
                sc_metal_compositor_destroy(compositor);
                return NULL;
            }
            return compositor;
        } @catch (NSException *exception) {
            sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while creating the compositor");
            return NULL;
        }
    }
}

void sc_metal_compositor_destroy(sc_metal_compositor *compositor) {
    if (compositor == NULL) return;
    @autoreleasepool {
        [compositor->command_buffer release];
        [compositor->readback release];
        [compositor->target release];
        [compositor->queue release];
        free(compositor);
    }
}

int sc_metal_compositor_begin(sc_metal_compositor *compositor, char **error_message) {
    @autoreleasepool {
        if (compositor == NULL || compositor->command_buffer != nil) {
            return sc_compositor_fail(error_message, @"invalid Metal compositor frame state");
        }
        compositor->command_buffer = [[compositor->queue commandBuffer] retain];
        if (compositor->command_buffer == nil) {
            return sc_compositor_fail(error_message, @"could not create Metal compositor command buffer");
        }
        return 1;
    }
}

int sc_metal_compositor_clear(sc_metal_compositor *compositor, uint8_t r, uint8_t g, uint8_t b, uint8_t a, char **error_message) {
    @autoreleasepool {
        @try {
            if (compositor == NULL || compositor->command_buffer == nil) {
                return sc_compositor_fail(error_message, @"no active Metal compositor frame");
            }
            sc_clear_uniforms uniforms = {
                .red = (float)r / 255.0f,
                .green = (float)g / 255.0f,
                .blue = (float)b / 255.0f,
                .alpha = (float)a / 255.0f,
                .width = (uint32_t)compositor->width,
                .height = (uint32_t)compositor->height,
            };
            id<MTLComputeCommandEncoder> encoder = [compositor->command_buffer computeCommandEncoder];
            if (encoder == nil) return sc_compositor_fail(error_message, @"could not create Metal clear encoder");
            [encoder setComputePipelineState:sc_clear_pipeline];
            [encoder setTexture:compositor->target atIndex:0];
            [encoder setBytes:&uniforms length:sizeof(uniforms) atIndex:0];
            [encoder dispatchThreads:MTLSizeMake(compositor->width, compositor->height, 1) threadsPerThreadgroup:sc_threadgroup_size(sc_clear_pipeline)];
            [encoder endEncoding];
            return 1;
        } @catch (NSException *exception) {
            return sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while clearing the compositor");
        }
    }
}

sc_metal_texture *sc_metal_compositor_upload(sc_metal_compositor *compositor, const uint8_t *pixels, int width, int height, int stride, char **error_message) {
    @autoreleasepool {
        @try {
            if (compositor == NULL || pixels == NULL || width <= 0 || height <= 0 || stride < width * 4) {
                sc_compositor_fail(error_message, @"invalid Metal sprite upload");
                return NULL;
            }
            MTLTextureDescriptor *descriptor = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatRGBA8Unorm width:(NSUInteger)width height:(NSUInteger)height mipmapped:NO];
            descriptor.storageMode = MTLStorageModeShared;
            descriptor.usage = MTLTextureUsageShaderRead;
            id<MTLTexture> texture = [sc_compositor_device newTextureWithDescriptor:descriptor];
            if (texture == nil) {
                sc_compositor_fail(error_message, @"could not allocate Metal sprite texture");
                return NULL;
            }
            [texture replaceRegion:MTLRegionMake2D(0, 0, width, height) mipmapLevel:0 withBytes:pixels bytesPerRow:(NSUInteger)stride];
            return (sc_metal_texture *)texture;
        } @catch (NSException *exception) {
            sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while uploading a sprite");
            return NULL;
        }
    }
}

void sc_metal_texture_release(sc_metal_texture *texture) {
    if (texture == NULL) return;
    [(id<MTLTexture>)texture release];
}

sc_metal_texture *sc_metal_compositor_surface_create(sc_metal_compositor *compositor, char **error_message) {
    @autoreleasepool {
        @try {
            if (compositor == NULL) {
                sc_compositor_fail(error_message, @"invalid Metal compositor surface");
                return NULL;
            }
            MTLTextureDescriptor *descriptor = [MTLTextureDescriptor texture2DDescriptorWithPixelFormat:MTLPixelFormatRGBA8Unorm
                                                                                                     width:(NSUInteger)compositor->width
                                                                                                    height:(NSUInteger)compositor->height
                                                                                                 mipmapped:NO];
            descriptor.storageMode = MTLStorageModePrivate;
            descriptor.usage = MTLTextureUsageShaderRead | MTLTextureUsageShaderWrite;
            id<MTLTexture> surface = [sc_compositor_device newTextureWithDescriptor:descriptor];
            if (surface == nil) {
                sc_compositor_fail(error_message, @"could not allocate Metal compositor surface");
                return NULL;
            }
            return (sc_metal_texture *)surface;
        } @catch (NSException *exception) {
            sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while creating a surface");
            return NULL;
        }
    }
}

int sc_metal_compositor_surface_clear(
    sc_metal_compositor *compositor,
    sc_metal_texture *surface,
    uint8_t r,
    uint8_t g,
    uint8_t b,
    uint8_t a,
    char **error_message
) {
    @autoreleasepool {
        @try {
            if (compositor == NULL || compositor->command_buffer == nil || surface == NULL) {
                return sc_compositor_fail(error_message, @"invalid Metal compositor surface clear");
            }
            sc_clear_uniforms uniforms = {
                .red = (float)r / 255.0f,
                .green = (float)g / 255.0f,
                .blue = (float)b / 255.0f,
                .alpha = (float)a / 255.0f,
                .width = (uint32_t)compositor->width,
                .height = (uint32_t)compositor->height,
            };
            id<MTLComputeCommandEncoder> encoder = [compositor->command_buffer computeCommandEncoder];
            if (encoder == nil) return sc_compositor_fail(error_message, @"could not create Metal surface clear encoder");
            [encoder setComputePipelineState:sc_clear_pipeline];
            [encoder setTexture:(id<MTLTexture>)surface atIndex:0];
            [encoder setBytes:&uniforms length:sizeof(uniforms) atIndex:0];
            [encoder dispatchThreads:MTLSizeMake(compositor->width, compositor->height, 1)
                 threadsPerThreadgroup:sc_threadgroup_size(sc_clear_pipeline)];
            [encoder endEncoding];
            return 1;
        } @catch (NSException *exception) {
            return sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while clearing a surface");
        }
    }
}

int sc_metal_compositor_mask_combine(
    sc_metal_compositor *compositor,
    sc_metal_texture *first,
    sc_metal_texture *second,
    sc_metal_texture *output,
    char **error_message
) {
    @autoreleasepool {
        @try {
            if (compositor == NULL || compositor->command_buffer == nil || first == NULL || second == NULL || output == NULL) {
                return sc_compositor_fail(error_message, @"invalid Metal mask combination");
            }
            sc_mask_uniforms uniforms = {
                .width = (uint32_t)compositor->width,
                .height = (uint32_t)compositor->height,
            };
            id<MTLComputeCommandEncoder> encoder = [compositor->command_buffer computeCommandEncoder];
            if (encoder == nil) return sc_compositor_fail(error_message, @"could not create Metal mask encoder");
            [encoder setComputePipelineState:sc_mask_pipeline];
            [encoder setTexture:(id<MTLTexture>)first atIndex:0];
            [encoder setTexture:(id<MTLTexture>)second atIndex:1];
            [encoder setTexture:(id<MTLTexture>)output atIndex:2];
            [encoder setBytes:&uniforms length:sizeof(uniforms) atIndex:0];
            [encoder dispatchThreads:MTLSizeMake(compositor->width, compositor->height, 1)
                 threadsPerThreadgroup:sc_threadgroup_size(sc_mask_pipeline)];
            [encoder endEncoding];
            return 1;
        } @catch (NSException *exception) {
            return sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while combining masks");
        }
    }
}

int sc_metal_compositor_draw(
    sc_metal_compositor *compositor,
    sc_metal_texture *destination,
    sc_metal_texture *texture,
    sc_metal_texture *alpha_mask,
    float inverse_a,
    float inverse_b,
    float inverse_c,
    float inverse_d,
    float inverse_tx,
    float inverse_ty,
    float red_add,
    float green_add,
    float blue_add,
    float alpha_mul,
    float red_mul,
    float green_mul,
    float blue_mul,
    uint32_t blend_mode,
    uint32_t allow_additive_coverage,
    uint32_t luminance_floor,
    uint32_t nrgba_over_fast_path,
    int left,
    int top,
    int right,
    int bottom,
    char **error_message
) {
    @autoreleasepool {
        @try {
            if (compositor == NULL || compositor->command_buffer == nil || texture == NULL || right <= left || bottom <= top) {
                return sc_compositor_fail(error_message, @"invalid Metal compositor draw");
            }
            id<MTLTexture> sprite = (id<MTLTexture>)texture;
            id<MTLTexture> target = destination == NULL ? compositor->target : (id<MTLTexture>)destination;
            sc_draw_uniforms uniforms = {
                .inverse_a = inverse_a,
                .inverse_b = inverse_b,
                .inverse_c = inverse_c,
                .inverse_d = inverse_d,
                .inverse_tx = inverse_tx,
                .inverse_ty = inverse_ty,
                .red_add = red_add,
                .green_add = green_add,
                .blue_add = blue_add,
                .alpha_mul = alpha_mul,
                .red_mul = red_mul,
                .green_mul = green_mul,
                .blue_mul = blue_mul,
                .blend_mode = blend_mode,
                .allow_additive_coverage = allow_additive_coverage,
                .luminance_floor = luminance_floor,
                .nrgba_over_fast_path = nrgba_over_fast_path,
                .left = (int32_t)left,
                .top = (int32_t)top,
                .width = (uint32_t)(right - left),
                .height = (uint32_t)(bottom - top),
                .sprite_width = (uint32_t)sprite.width,
                .sprite_height = (uint32_t)sprite.height,
                .has_mask = alpha_mask == NULL ? 0u : 1u,
            };
            id<MTLComputeCommandEncoder> encoder = [compositor->command_buffer computeCommandEncoder];
            if (encoder == nil) return sc_compositor_fail(error_message, @"could not create Metal compositor encoder");
            [encoder setComputePipelineState:sc_draw_pipeline];
            [encoder setTexture:sprite atIndex:0];
            [encoder setTexture:target atIndex:1];
            [encoder setTexture:(id<MTLTexture>)alpha_mask atIndex:2];
            [encoder setBytes:&uniforms length:sizeof(uniforms) atIndex:0];
            [encoder dispatchThreads:MTLSizeMake(uniforms.width, uniforms.height, 1) threadsPerThreadgroup:sc_threadgroup_size(sc_draw_pipeline)];
            [encoder endEncoding];
            return 1;
        } @catch (NSException *exception) {
            return sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while drawing a sprite");
        }
    }
}

int sc_metal_compositor_readback(sc_metal_compositor *compositor, uint8_t *pixels, int stride, char **error_message) {
    @autoreleasepool {
        @try {
            if (compositor == NULL || compositor->command_buffer == nil || pixels == NULL || stride < compositor->width * 4) {
                return sc_compositor_fail(error_message, @"invalid Metal compositor readback");
            }
            id<MTLBlitCommandEncoder> blit = [compositor->command_buffer blitCommandEncoder];
            if (blit == nil) return sc_compositor_fail(error_message, @"could not create Metal compositor readback encoder");
            [blit copyFromTexture:compositor->target
                      sourceSlice:0
                      sourceLevel:0
                     sourceOrigin:MTLOriginMake(0, 0, 0)
                       sourceSize:MTLSizeMake(compositor->width, compositor->height, 1)
                         toBuffer:compositor->readback
                destinationOffset:0
           destinationBytesPerRow:compositor->readback_bytes_per_row
         destinationBytesPerImage:compositor->readback_bytes_per_row * (NSUInteger)compositor->height];
            [blit endEncoding];
            [compositor->command_buffer commit];
            [compositor->command_buffer waitUntilCompleted];
            int result = 1;
            if (compositor->command_buffer.status == MTLCommandBufferStatusError) {
                result = sc_compositor_fail(error_message, compositor->command_buffer.error.localizedDescription ?: @"Metal compositor command failed");
            } else {
                const uint8_t *source = (const uint8_t *)compositor->readback.contents;
                for (int y = 0; y < compositor->height; y++) {
                    memcpy(pixels + (size_t)y * (size_t)stride, source + (size_t)y * compositor->readback_bytes_per_row, (size_t)compositor->width * 4);
                }
            }
            [compositor->command_buffer release];
            compositor->command_buffer = nil;
            return result;
        } @catch (NSException *exception) {
            [compositor->command_buffer release];
            compositor->command_buffer = nil;
            return sc_compositor_fail(error_message, exception.reason ?: @"Metal raised an exception while reading the compositor");
        }
    }
}
