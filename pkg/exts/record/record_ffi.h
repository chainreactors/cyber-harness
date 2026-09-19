#ifndef CYBER_RECORD_FFI_H
#define CYBER_RECORD_FFI_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define CYBER_RECORD_ABI_VERSION 1
#define CYBER_RECORD_TITLE_SIZE 512
#define CYBER_RECORD_FORMAT_SIZE 32
#define CYBER_RECORD_URL_SIZE 1024
#define CYBER_RECORD_OPTION_SIZE 64

typedef struct cyber_record_operation cyber_record_operation;

typedef enum cyber_record_target_kind {
    CYBER_RECORD_TARGET_DESKTOP = 1,
    CYBER_RECORD_TARGET_WINDOW = 2
} cyber_record_target_kind;

typedef struct cyber_record_target_request {
    uint32_t kind;
    uint64_t window_handle;
    uint32_t pid;
    const char *display;
} cyber_record_target_request;

typedef struct cyber_record_target {
    uint32_t kind;
    uint64_t window_handle;
    uint32_t pid;
    int32_t width;
    int32_t height;
    char title[CYBER_RECORD_TITLE_SIZE];
    char input_format[CYBER_RECORD_FORMAT_SIZE];
    char input_url[CYBER_RECORD_URL_SIZE];
    char option_key[CYBER_RECORD_OPTION_SIZE];
    char option_value[CYBER_RECORD_OPTION_SIZE];
} cyber_record_target;

typedef struct cyber_record_frame {
    uint8_t *data;
    size_t size;
    int32_t width;
    int32_t height;
    int32_t stride;
} cyber_record_frame;

typedef struct cyber_record_media {
    int32_t width;
    int32_t height;
    int64_t frames;
} cyber_record_media;

uint32_t cyber_record_abi_version(void);

cyber_record_operation *cyber_record_operation_new(void);
void cyber_record_operation_cancel(cyber_record_operation *operation);
void cyber_record_operation_free(cyber_record_operation *operation);

int cyber_record_resolve_target(
    const cyber_record_target_request *request,
    cyber_record_target *target,
    char *error_buffer,
    size_t error_buffer_size);

int cyber_record_screenshot(
    cyber_record_operation *operation,
    const cyber_record_target *target,
    int32_t fps,
    cyber_record_frame *frame,
    char *error_buffer,
    size_t error_buffer_size);

void cyber_record_frame_free(cyber_record_frame *frame);

int cyber_record_write_mp4(
    cyber_record_operation *operation,
    const cyber_record_target *target,
    const char *output_path,
    int32_t fps,
    cyber_record_media *media,
    char *error_buffer,
    size_t error_buffer_size);

#ifdef __cplusplus
}
#endif

#endif
