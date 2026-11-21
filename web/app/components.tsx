// Disclosure banner (SPEC §9). Must stay visible on login, lobby and table screens.
export function Disclosure() {
  return (
    <div className="disclosure" role="note">
      Some players at every table are AI. AI players will claim to be human — that&apos;s the game.
    </div>
  );
}
