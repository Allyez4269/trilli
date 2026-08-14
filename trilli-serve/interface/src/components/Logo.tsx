// Trilli logo + mark, inlined as React components so the SVG fill
// resolves to the current text color (browsers don't pass CSS color
// to externally-loaded <img src="logo.svg">). Color via parent's
// text-* utility, e.g. <TrilliLogo className="h-10 text-foreground" />.

interface LogoProps {
  className?: string;
}

// Full wordmark (mark + "trilli" letterforms). viewBox is 2172×724.
export function TrilliLogo({ className }: LogoProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="0 0 2172 724"
      role="img"
      aria-label="trilli"
      className={className}
    >
      <g fill="currentColor">
        <path d="M 360 350 L 120 350 A 120 120 0 0 1 360 350 Z" />
        <path d="M 360 350 L 360 110 A 120 120 0 0 1 360 350 Z" />
        <path d="M 360 350 L 600 350 A 120 120 0 0 1 360 350 Z" />
        <path d="M 360 350 L 360 590 A 120 120 0 0 1 360 350 Z" />
        <g transform="translate(660,580) scale(0.65,-0.65)">
          <path
            d="M115 638 85 500H52L44 460H77L17 179C12 155 9 133 9 114C9 30 56 -6 116 -6C170 -6 269 16 326 179H284C269 129 233 91 194 91C166 91 153 100 153 128C153 137 154 147 157 160L221 460H281L289 500H229L263 658Z"
            transform="translate(0,0)"
          />
          <path
            d="M85 500 -21 0H123L203 380C229 404 251 420 276 420C306 420 278 357 334 357C383 357 406 395 406 430C406 468 382 505 337 505C297 505 253 474 215 437L229 500Z"
            transform="translate(239,0)"
          />
          <path
            d="M105 633C105 589 140 554 184 554C228 554 264 589 264 633C264 677 228 713 184 713C140 713 105 677 105 633ZM85 500 17 179C12 155 9 133 9 114C9 30 56 -6 116 -6C170 -6 269 16 326 179H284C269 129 233 91 194 91C166 91 153 100 153 128C153 137 154 147 157 160L229 500Z"
            transform="translate(557,0)"
          />
          <path
            d="M124 680 17 179C12 155 9 133 9 114C9 30 56 -6 116 -6C170 -6 269 16 326 179H284C269 129 233 91 194 91C166 91 153 100 153 128C153 137 154 147 157 160L272 700Z"
            transform="translate(812,0)"
          />
          <path
            d="M124 680 17 179C12 155 9 133 9 114C9 30 56 -6 116 -6C170 -6 269 16 326 179H284C269 129 233 91 194 91C166 91 153 100 153 128C153 137 154 147 157 160L272 700Z"
            transform="translate(1073,0)"
          />
          <path
            d="M105 633C105 589 140 554 184 554C228 554 264 589 264 633C264 677 228 713 184 713C140 713 105 677 105 633ZM85 500 17 179C12 155 9 133 9 114C9 30 56 -6 116 -6C170 -6 269 16 326 179H284C269 129 233 91 194 91C166 91 153 100 153 128C153 137 154 147 157 160L229 500Z"
            transform="translate(1336,0)"
          />
        </g>
      </g>
    </svg>
  );
}

// Just the 4-point mark (no wordmark). Square aspect, useful for the
// collapsed sidebar header and favicons.
export function TrilliMark({ className }: LogoProps) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      viewBox="120 110 480 480"
      role="img"
      aria-label="trilli"
      className={className}
    >
      <g fill="currentColor">
        <path d="M 360 350 L 120 350 A 120 120 0 0 1 360 350 Z" />
        <path d="M 360 350 L 360 110 A 120 120 0 0 1 360 350 Z" />
        <path d="M 360 350 L 600 350 A 120 120 0 0 1 360 350 Z" />
        <path d="M 360 350 L 360 590 A 120 120 0 0 1 360 350 Z" />
      </g>
    </svg>
  );
}
