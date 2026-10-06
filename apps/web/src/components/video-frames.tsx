'use client';
import { useEffect, useRef, useState } from 'react';
import { Film, Camera, X } from 'lucide-react';
import { FormError } from './ui';

type Frame = { file: File; url: string; seconds: number };

// Only an explicitly selected still is passed to the existing private uploader.
// The recorded clip remains a browser object URL and never leaves the device.
export function VideoFrames({
  disabled,
  onUse,
}: {
  disabled: boolean;
  onUse: (file: File) => Promise<void>;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const epoch = useRef(0);
  const [clip, setClip] = useState<string | null>(null);
  const [frame, setFrame] = useState<Frame | null>(null);
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  useEffect(
    () => () => {
      if (clip) URL.revokeObjectURL(clip);
    },
    [clip],
  );
  useEffect(
    () => () => {
      if (frame) URL.revokeObjectURL(frame.url);
    },
    [frame],
  );
  useEffect(() => {
    const element = video.current;
    function pause() {
      if (document.hidden) element?.pause();
    }
    document.addEventListener('visibilitychange', pause);
    return () => {
      epoch.current++;
      element?.pause();
      document.removeEventListener('visibilitychange', pause);
    };
  }, [clip]);

  function close() {
    epoch.current++;
    video.current?.pause();
    setClip(null);
    setFrame(null);
    setReady(false);
    setBusy(false);
  }
  async function capture() {
    const element = video.current;
    if (!element || !ready || disabled || busy || element.seeking || element.readyState < 2) return;
    element.pause();
    const version = epoch.current;
    const seconds = element.currentTime;
    setBusy(true);
    setError(null);
    try {
      const scale = Math.min(1, 1600 / Math.max(element.videoWidth, element.videoHeight));
      const canvas = document.createElement('canvas');
      canvas.width = Math.max(1, Math.round(element.videoWidth * scale));
      canvas.height = Math.max(1, Math.round(element.videoHeight * scale));
      const ctx = canvas.getContext('2d');
      if (!ctx) throw new Error('Frame capture is unavailable. Add a photo instead.');
      ctx.drawImage(element, 0, 0, canvas.width, canvas.height);
      const blob = await new Promise<Blob | null>((resolve) =>
        canvas.toBlob(resolve, 'image/jpeg', 0.9),
      );
      if (version !== epoch.current) return;
      if (!blob)
        throw new Error('This frame could not be read. Choose another frame or add a photo.');
      setFrame({
        file: new File([blob], 'road-frame.jpg', { type: 'image/jpeg' }),
        url: URL.createObjectURL(blob),
        seconds,
      });
    } catch (e) {
      if (version === epoch.current) setError(e as Error);
    } finally {
      if (version === epoch.current) setBusy(false);
    }
  }
  return (
    <div className="video-frames" aria-label="Recorded drive footage">
      <button
        type="button"
        className="secondary small"
        disabled={disabled || busy}
        onClick={() => input.current?.click()}
      >
        <Film size={16} /> Select frame from video
      </button>
      <input
        ref={input}
        hidden
        type="file"
        accept="video/mp4,video/webm,video/quicktime"
        aria-label="Choose recorded road video"
        onChange={(e) => {
          const file = e.target.files?.[0];
          e.target.value = '';
          if (!file) return;
          close();
          setError(null);
          if (
            !['video/mp4', 'video/webm', 'video/quicktime'].includes(file.type) ||
            file.size < 1 ||
            file.size > 100 * 1024 * 1024
          ) {
            setError(
              new Error('Choose an MP4, WebM or MOV clip up to 100 MiB. You can also add a photo.'),
            );
            return;
          }
          setClip(URL.createObjectURL(file));
        }}
      />
      <FormError error={error} />
      {clip && (
        <div className="video-frame-picker">
          <p className="photo-help">
            Review recorded footage when parked. This clip stays on your device. Only a frame you
            choose to attach is uploaded.
          </p>
          <video
            key={clip}
            ref={video}
            src={clip}
            controls
            muted
            playsInline
            preload="metadata"
            aria-label="Local road video preview"
            onError={() => {
              close();
              setError(
                new Error('This video format could not be read. Try another clip or add a photo.'),
              );
            }}
            onLoadedMetadata={(e) => {
              const v = e.currentTarget;
              if (
                !Number.isFinite(v.duration) ||
                v.duration <= 0 ||
                v.duration > 600 ||
                v.videoWidth < 1 ||
                v.videoHeight < 1 ||
                v.videoWidth > 8192 ||
                v.videoHeight > 8192 ||
                v.videoWidth * v.videoHeight > 12000000
              ) {
                close();
                setError(
                  new Error(
                    'Choose a clip up to 10 minutes and 12 megapixels per frame, or add a photo.',
                  ),
                );
                return;
              }
              setReady(v.readyState >= 2);
            }}
            onLoadedData={() => setReady(true)}
            onSeeking={() => setReady(false)}
            onSeeked={(e) => setReady(e.currentTarget.readyState >= 2)}
          />
          <div className="photo-capture-actions">
            <button
              type="button"
              className="secondary small"
              disabled={!ready || busy || disabled}
              onClick={() => void capture()}
            >
              <Camera size={16} /> Review this frame
            </button>
            <button type="button" className="text-button" disabled={busy} onClick={close}>
              <X size={16} /> Close video
            </button>
          </div>
          {frame && (
            <div className="selected-road-frame">
              <img
                src={frame.url}
                alt={`Selected road frame at ${frame.seconds.toFixed(1)} seconds`}
              />
              <p className="muted">
                Frame at {frame.seconds.toFixed(1)} seconds. Check that the road issue and landmark
                are visible.
              </p>
              <button
                type="button"
                className="primary small"
                disabled={busy || disabled}
                onClick={async () => {
                  setBusy(true);
                  try {
                    await onUse(frame.file);
                    close();
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Attach reviewed frame
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
