// Procedural symmetric 16×16 creature from a seed (SPEC §8), ported from design/game-room/shared.js.
// Original pixel art only; the 16-colour palette from globals.css.

const HEX: Record<string, string> = {
  ink: "#1b1220", parch: "#f1e2bc", gold: "#e9b44c", red: "#c2503c", green: "#6fa04a", blue: "#3f78a8",
  purple: "#7d56a0", teal: "#3e9c94", white: "#fff6e0", pink: "#d9739a", sky: "#8fc3d9",
};
const BODY = ["red", "blue", "green", "purple", "teal", "pink", "gold", "sky"];

function mulberry32(a: number) {
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function drawSprite(cv: HTMLCanvasElement, seed: number) {
  const r = mulberry32(seed + 1);
  const ctx = cv.getContext("2d");
  if (!ctx) return;
  ctx.imageSmoothingEnabled = false;
  ctx.clearRect(0, 0, 16, 16);
  const body = HEX[BODY[Math.floor(r() * BODY.length)]];
  const accent = HEX[BODY[Math.floor(r() * BODY.length)]];
  const half: (string | null)[][] = [];
  for (let y = 0; y < 16; y++) {
    half[y] = [];
    for (let x = 0; x < 8; x++) {
      const on = y >= 3 && y <= 14 && r() < 0.92 - Math.abs(y - 8) * 0.04 - x * 0.03;
      half[y][x] = on ? (r() < 0.18 ? accent : body) : null;
    }
  }
  if (r() < 0.6) {
    half[2][1] = accent;
    half[2][6] = accent;
  }
  half[6][3] = HEX.white; // eye
  half[10][3] = HEX.ink; // mouth
  for (let y = 0; y < 16; y++) {
    for (let x = 0; x < 16; x++) {
      const c = x < 8 ? half[y][x] : half[y][15 - x];
      if (c) {
        ctx.fillStyle = c;
        ctx.fillRect(x, y, 1, 1);
      }
    }
  }
}
