// frontend/src/lib/mediaValidator.js

/**
 * Attempts to load an image URL in a hidden DOM element.
 * Resolves to true if it loads successfully, false otherwise.
 */
export function validateImage(url) {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(true);
    img.onerror = () => resolve(false);
    img.src = url;
  });
}

/**
 * Attempts to load a video URL's metadata in a hidden DOM element.
 * Resolves to true if it loads successfully, false otherwise.
 */
export function validateVideo(url) {
  return new Promise((resolve) => {
    const video = document.createElement('video');
    // For videos, loading metadata is enough to prove it's a valid playable video file
    video.onloadedmetadata = () => resolve(true);
    video.onerror = () => resolve(false);
    video.src = url;
  });
}
