import isodoIcon from "../assets/isodo/icon.png";

export function SplashScreen() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <div className="flex size-24 items-center justify-center" aria-label="Isodo loading">
        <img alt="Isodo" className="size-16 object-contain" src={isodoIcon} />
      </div>
    </div>
  );
}
