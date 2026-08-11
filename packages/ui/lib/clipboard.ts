/**
 * Copy text to the clipboard, with a fallback for insecure contexts (plain http://).
 *
 * The async Clipboard API (`navigator.clipboard`) is only exposed in a secure
 * context — `https://` or `localhost`. On a plain `http://` origin it is
 * `undefined`, so `navigator.clipboard.writeText` throws and the copy silently
 * fails (the symptom behind self-hosted-over-http bug reports). When the secure
 * API is unavailable we fall back to a hidden `<textarea>` + the legacy
 * `document.execCommand('copy')`, which works in non-secure contexts.
 *
 * @returns `true` on success, `false` on failure. Callers should gate their
 * success side effects (toast, "copied" check state) on the return value and
 * surface an error when it is `false`.
 */
export async function copyText(text: string): Promise<boolean> {
  // Preferred path: async Clipboard API (secure contexts only).
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Permission denied / document not focused / blocked — fall through to
      // the legacy path below rather than failing outright.
    }
  }

  // Fallback: hidden textarea + execCommand('copy'). Works over plain http://.
  if (typeof document === "undefined") return false;

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "");
  // Keep it visually hidden and out of layout/scroll flow.
  textarea.style.position = "fixed";
  textarea.style.top = "0";
  textarea.style.left = "0";
  textarea.style.width = "1px";
  textarea.style.height = "1px";
  textarea.style.padding = "0";
  textarea.style.border = "none";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";

  // Preserve focus so an open menu/popover that owns the copy button is not
  // disturbed by the temporary selection.
  const previouslyFocused =
    document.activeElement instanceof HTMLElement ? document.activeElement : null;

  document.body.appendChild(textarea);
  try {
    textarea.focus();
    textarea.select();
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    document.body.removeChild(textarea);
    previouslyFocused?.focus();
  }
}

/**
 * Copy an image to the system clipboard as PNG.
 *
 * The ClipboardItem is created immediately from a promise so Safari and
 * Chromium keep the original click's user activation while attachment bytes
 * are fetched and, when necessary, converted from JPEG/WebP to PNG.
 */
export async function copyImage(
  source: Blob | Promise<Blob>,
): Promise<boolean> {
  if (
    typeof navigator === "undefined" ||
    !navigator.clipboard?.write ||
    typeof ClipboardItem === "undefined"
  ) {
    return false;
  }

  try {
    const png = Promise.resolve(source).then(toPngBlob);
    await navigator.clipboard.write([
      new ClipboardItem({ "image/png": png }),
    ]);
    return true;
  } catch {
    return false;
  }
}

async function toPngBlob(blob: Blob): Promise<Blob> {
  if (blob.type === "image/png") return blob;
  if (typeof createImageBitmap !== "function" || typeof document === "undefined") {
    throw new Error("Image conversion is unavailable");
  }

  const bitmap = await createImageBitmap(blob);
  try {
    const canvas = document.createElement("canvas");
    canvas.width = bitmap.width;
    canvas.height = bitmap.height;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Canvas is unavailable");
    context.drawImage(bitmap, 0, 0);
    const png = await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        (result) =>
          result ? resolve(result) : reject(new Error("PNG conversion failed")),
        "image/png",
      );
    });
    return png;
  } finally {
    bitmap.close();
  }
}
