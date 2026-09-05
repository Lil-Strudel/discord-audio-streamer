// Telemetry has no generated binding because it is only ever pushed as an
// event, never returned from a bound method. Keep this in step with the Go
// struct of the same name in app.go.
export interface Telemetry {
  positionMs: number;
  rms: number;
  peak: number;
  bufferedFrames: number;
  bufferCapacity: number;
  droppedFrames: number;
  underruns: number;
  framesSent: number;
  framesHeld: number;
  resyncs: number;
  latenessMs: number;
  maxLatenessMs: number;
  encryptionReady: boolean;
}

export const emptyTelemetry: Telemetry = {
  positionMs: 0,
  rms: 0,
  peak: 0,
  bufferedFrames: 0,
  bufferCapacity: 0,
  droppedFrames: 0,
  underruns: 0,
  framesSent: 0,
  framesHeld: 0,
  resyncs: 0,
  latenessMs: 0,
  maxLatenessMs: 0,
  encryptionReady: false,
};

/** Formats milliseconds as m:ss, or h:mm:ss for anything over an hour. */
export function formatTime(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) ms = 0;
  const total = Math.floor(ms / 1000);
  const seconds = total % 60;
  const minutes = Math.floor(total / 60) % 60;
  const hours = Math.floor(total / 3600);

  const pad = (n: number) => String(n).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${minutes}:${pad(seconds)}`;
}

/** Turns any thrown value from a Wails call into something displayable. */
export function errorText(err: unknown): string {
  if (typeof err === "string") return err;
  if (err instanceof Error) return err.message;
  return String(err);
}
