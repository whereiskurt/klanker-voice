'use client';

/**
 * Ambient background layer.
 *
 * run.auth's version rendered a parallax Vegas map (DEF CON venue imagery)
 * loaded from /public/bg/vegas-z*.png with a settings gear to adjust
 * zoom/opacity/parallax. Those images are DEF CON-specific and were dropped
 * during the port (D-09); this is a lightweight CSS gradient replacement
 * with no image assets and no client-side settings state.
 */
export function MapBackground() {
  return (
    <div
      aria-hidden
      className="fixed inset-0 z-0 pointer-events-none"
      style={{
        background:
          'radial-gradient(1200px circle at 50% -10%, rgba(104,110,160,0.18), transparent 60%)',
      }}
    />
  );
}
