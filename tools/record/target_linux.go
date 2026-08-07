//go:build record_ffmpeg && cgo && linux

package record

/*
#cgo pkg-config: xcb
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <xcb/xcb.h>

typedef struct {
    uint32_t window;
    uint32_t pid;
    uint32_t width;
    uint32_t height;
    char title[512];
} aiscan_x11_window;

static xcb_atom_t aiscan_atom(xcb_connection_t *c, const char *name) {
    xcb_intern_atom_cookie_t cookie = xcb_intern_atom(c, 0, strlen(name), name);
    xcb_intern_atom_reply_t *reply = xcb_intern_atom_reply(c, cookie, NULL);
    if (!reply) return XCB_ATOM_NONE;
    xcb_atom_t atom = reply->atom;
    free(reply);
    return atom;
}

static xcb_screen_t *aiscan_screen(xcb_connection_t *c, int number) {
    const xcb_setup_t *setup = xcb_get_setup(c);
    xcb_screen_iterator_t it = xcb_setup_roots_iterator(setup);
    for (int i = 0; i < number && it.rem; i++) xcb_screen_next(&it);
    return it.rem ? it.data : NULL;
}

static int aiscan_window_info(xcb_connection_t *c, xcb_window_t window,
                              xcb_atom_t pid_atom, xcb_atom_t name_atom,
                              aiscan_x11_window *out) {
    xcb_get_window_attributes_reply_t *attrs = xcb_get_window_attributes_reply(
        c, xcb_get_window_attributes(c, window), NULL);
    if (!attrs || attrs->map_state != XCB_MAP_STATE_VIEWABLE) {
        free(attrs);
        return 0;
    }
    free(attrs);
    xcb_get_geometry_reply_t *geometry = xcb_get_geometry_reply(c, xcb_get_geometry(c, window), NULL);
    if (!geometry || geometry->width == 0 || geometry->height == 0) {
        free(geometry);
        return 0;
    }
    memset(out, 0, sizeof(*out));
    out->window = window;
    out->width = geometry->width;
    out->height = geometry->height;
    free(geometry);

    if (pid_atom != XCB_ATOM_NONE) {
        xcb_get_property_reply_t *pid_reply = xcb_get_property_reply(c,
            xcb_get_property(c, 0, window, pid_atom, XCB_ATOM_CARDINAL, 0, 1), NULL);
        if (pid_reply && xcb_get_property_value_length(pid_reply) >= 4) {
            out->pid = *(uint32_t *)xcb_get_property_value(pid_reply);
        }
        free(pid_reply);
    }
    if (name_atom != XCB_ATOM_NONE) {
        xcb_get_property_reply_t *name_reply = xcb_get_property_reply(c,
            xcb_get_property(c, 0, window, name_atom, XCB_GET_PROPERTY_TYPE_ANY, 0, 511), NULL);
        if (name_reply) {
            int length = xcb_get_property_value_length(name_reply);
            if (length > 511) length = 511;
            if (length > 0) memcpy(out->title, xcb_get_property_value(name_reply), length);
            out->title[length] = 0;
        }
        free(name_reply);
    }
    return 1;
}

// Returns 0 on success, 1 on connection error, 2 when no matching window exists.
static int aiscan_x11_resolve(const char *display, uint32_t requested_window,
                              uint32_t requested_pid, aiscan_x11_window *out,
                              uint32_t *screen_width, uint32_t *screen_height) {
    int screen_number = 0;
    xcb_connection_t *c = xcb_connect(display && display[0] ? display : NULL, &screen_number);
    if (!c || xcb_connection_has_error(c)) {
        if (c) xcb_disconnect(c);
        return 1;
    }
    xcb_screen_t *screen = aiscan_screen(c, screen_number);
    if (!screen) {
        xcb_disconnect(c);
        return 1;
    }
    *screen_width = screen->width_in_pixels;
    *screen_height = screen->height_in_pixels;
    xcb_atom_t pid_atom = aiscan_atom(c, "_NET_WM_PID");
    xcb_atom_t name_atom = aiscan_atom(c, "_NET_WM_NAME");

    if (requested_window) {
        int ok = aiscan_window_info(c, requested_window, pid_atom, name_atom, out);
        xcb_disconnect(c);
        return ok ? 0 : 2;
    }

    xcb_atom_t list_atom = aiscan_atom(c, "_NET_CLIENT_LIST");
    xcb_get_property_reply_t *list = xcb_get_property_reply(c,
        xcb_get_property(c, 0, screen->root, list_atom, XCB_ATOM_WINDOW, 0, UINT32_MAX), NULL);
    if (!list) {
        xcb_disconnect(c);
        return 2;
    }
    int count = xcb_get_property_value_length(list) / (int)sizeof(xcb_window_t);
    xcb_window_t *windows = (xcb_window_t *)xcb_get_property_value(list);
    uint64_t best_area = 0;
    aiscan_x11_window candidate;
    memset(out, 0, sizeof(*out));
    for (int i = 0; i < count; i++) {
        if (!aiscan_window_info(c, windows[i], pid_atom, name_atom, &candidate)) continue;
        if (candidate.pid != requested_pid) continue;
        uint64_t area = (uint64_t)candidate.width * candidate.height;
        if (area > best_area) {
            best_area = area;
            *out = candidate;
        }
    }
    free(list);
    xcb_disconnect(c);
    return out->window ? 0 : 2;
}
*/
import "C"

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

func resolvePlatformTarget(_ context.Context, req captureRequest) (resolvedTarget, error) {
	if req.WindowHandle > math.MaxUint32 {
		return resolvedTarget{}, fmt.Errorf("X11 window ID 0x%x exceeds 32 bits", req.WindowHandle)
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		return resolvedTarget{}, fmt.Errorf("Wayland capture is not supported; use an X11 session")
	}
	display := strings.TrimSpace(os.Getenv("DISPLAY"))
	if display == "" {
		return resolvedTarget{}, fmt.Errorf("DISPLAY is not set")
	}
	cDisplay := C.CString(display)
	defer C.free(unsafe.Pointer(cDisplay))
	var info C.aiscan_x11_window
	var screenWidth, screenHeight C.uint32_t
	code := C.aiscan_x11_resolve(
		cDisplay,
		C.uint32_t(req.WindowHandle),
		C.uint32_t(req.PID),
		&info,
		&screenWidth,
		&screenHeight,
	)
	if code == 1 {
		return resolvedTarget{}, fmt.Errorf("connect to X11 display %s", display)
	}
	if req.Target == "desktop" {
		return resolvedTarget{
			Info:   TargetInfo{Kind: "desktop", Width: int(screenWidth), Height: int(screenHeight)},
			Native: nativeCaptureTarget{format: "x11grab", url: display},
		}, nil
	}
	if code != 0 {
		if req.WindowHandle != 0 {
			return resolvedTarget{}, fmt.Errorf("X11 window 0x%x is missing, hidden, or minimized", req.WindowHandle)
		}
		return resolvedTarget{}, fmt.Errorf("no visible X11 top-level window found for pid %d", req.PID)
	}
	handle := uint64(info.window)
	return resolvedTarget{
		Info: TargetInfo{
			Kind:         "window",
			WindowHandle: fmt.Sprintf("0x%x", handle),
			PID:          int64(info.pid),
			Title:        C.GoString(&info.title[0]),
			Width:        int(info.width),
			Height:       int(info.height),
		},
		Native: nativeCaptureTarget{
			format: "x11grab",
			url:    display,
			options: map[string]string{
				"window_id": strconv.FormatUint(handle, 10),
			},
		},
	}, nil
}
