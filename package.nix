{pkgs ? import <nixpkgs> {}, ...}:
pkgs.buildGoModule rec {
  name = "alibot";
  version = "0.0.1";
  vendorSha256 = "sha256-4sfyHoGweyiHqMxbIw8Eq4iYIeHUBocp0wN0rwemils=";
  src = ./.;
  meta = with pkgs.lib; {
    description = "A bot to share aliexpress links";
    homepage = "https://github.com/lucasew/alibot";
    platforms = platforms.linux;
  };
}

