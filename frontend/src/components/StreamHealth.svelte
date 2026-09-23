<script lang="ts">
  import type { Telemetry } from "../lib/types";

  let {
    telemetry,
    live,
  }: {
    telemetry: Telemetry;
    // A file source blocks instead of dropping, so the dropped count only
    // means anything for live capture.
    live: boolean;
  } = $props();
</script>

<dl class="stats">
  <div><dt>Buffered</dt><dd>{telemetry.bufferedFrames} / {telemetry.bufferCapacity}</dd></div>
  {#if live}
    <div><dt>Dropped</dt><dd class:bad={telemetry.droppedFrames > 0}>{telemetry.droppedFrames}</dd></div>
  {/if}
  <div><dt>Underruns</dt><dd class:bad={telemetry.underruns > 0}>{telemetry.underruns}</dd></div>
  <div><dt>Frames sent</dt><dd>{telemetry.framesSent}</dd></div>
  <div><dt>Clock resyncs</dt><dd class:bad={telemetry.resyncs > 0}>{telemetry.resyncs}</dd></div>
  <div><dt>Worst lateness</dt><dd>{telemetry.maxLatenessMs.toFixed(1)} ms</dd></div>
</dl>
<p class="note">
  {#if live}
    Dropped frames mean audio arrived faster than it could be sent; underruns mean
    it arrived too slowly and silence was sent instead. A few of either around a
    start or stop is normal.
  {:else}
    Underruns mean the file was not decoded in time and silence was sent instead;
    clock resyncs mean this machine fell too far behind to keep the 20 ms cadence.
    A few underruns as a track starts is normal. If these stay clean while
    listeners still hear stutter, the cause is the network, not this machine.
  {/if}
</p>

<style>
  .stats {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 10px 16px;
    margin: 14px 0 0;
  }

  .stats div {
    display: flex;
    justify-content: space-between;
    gap: 8px;
    padding-bottom: 4px;
    border-bottom: 1px solid var(--border);
  }

  dt {
    color: var(--text-faint);
    font-size: 12.5px;
  }

  dd {
    margin: 0;
    font-variant-numeric: tabular-nums;
    font-size: 12.5px;
  }

  dd.bad {
    color: var(--warn);
  }

  .note {
    margin: 8px 0 0;
    font-size: 12.5px;
    color: var(--text-faint);
  }
</style>
