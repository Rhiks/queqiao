The Android and iOS apps can read a one-time invitation from a QR code with
the device camera, from the same import screen that takes a pasted one. Five
hundred characters of base64 were never going to be typed from another
screen. Android decodes the frame on the device in the Go core and iOS uses
the system's own detector; no image or invitation leaves the process, and a
scanned code that is not a valid Queqiao invitation is refused at the
viewfinder. The released Android build gains only the CAMERA permission,
which is optional.
