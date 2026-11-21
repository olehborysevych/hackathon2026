"use client";

import { useEffect, useRef } from "react";
import { drawSprite } from "@/lib/sprite";

// Disclosure banner (SPEC §9). Must stay visible on login, lobby and table screens.
export function Disclosure() {
  return (
    <div className="disclosure" role="note">
      Some players at every table are AI. AI players will claim to be human — that&apos;s the game.
    </div>
  );
}

// Sprite renders the seat's 16×16 creature, scaled up with image-rendering: pixelated.
export function Sprite({ seed, size = 56 }: { seed: number; size?: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    if (ref.current) drawSprite(ref.current, seed);
  }, [seed]);
  return <canvas ref={ref} width={16} height={16} className="sprite" style={{ width: size, height: size }} aria-hidden="true" />;
}
