#!/usr/bin/env bash
# build-slot.sh — semáforo de builds pesados, cross-sesión y bloqueante en el kernel.
#
# PROBLEMA: N sesiones concurrentes lanzando `docker build`/`docker run` de toolchain
# funden la máquina compartida (dato real: load 286 con 6 núcleos, agentes en busy-wait
# quemando tokens). SOLUCIÓN: cada build pesado adquiere un "slot" antes de correr; hay
# max(1, núcleos/4) slots por máquina. Si no hay slot libre, el proceso BLOQUEA en el
# kernel (flock LOCK_EX, cero polling) hasta que uno se libere.
#
# El lock lo sostiene UN proceso perl que hace `exec` del comando manteniendo abierto el
# fd del slot: el lock vive durante todo el build y el kernel lo libera al morir el
# proceso — incluso con kill -9. Jamás quedan locks huérfanos. Portable: perl y Fcntl
# :flock existen en macOS y Linux (macOS NO trae el binario flock(1)).
#
# Uso:  bash scripts/build-slot.sh <cmd> [args…]
#   Adquiere un slot y hace exec del comando (argv directo, sin `sh -c`; el exit code se
#   propaga por el exec). Override del número de slots: HARNESS_BUILD_SLOTS=<n>.
#   Re-entrante: si HARNESS_BUILD_SLOT_HELD=1 ya está en el entorno, hace exec directo
#   sin tomar lock (evita deadlock si un comando wrappeado invoca otro wrapper).
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
uso: build-slot.sh <cmd> [args…]
  Adquiere un slot de build (semáforo cross-sesión, bloqueo de kernel vía perl/flock)
  y hace exec del comando. Serializa builds pesados para no fundir la máquina compartida.
  Slots = HARNESS_BUILD_SLOTS (si es entero ≥1) o max(1, núcleos/4).
  Re-entrante vía HARNESS_BUILD_SLOT_HELD=1 (exec directo, sin lock).
EOF
}

# --- args / ayuda ---------------------------------------------------------------
if [ $# -eq 0 ]; then usage; exit 1; fi
case "$1" in -h|--help) usage; exit 0 ;; esac

# --- re-entrancia: ya tenemos un slot, no re-lockear (evita deadlock) -----------
if [ "${HARNESS_BUILD_SLOT_HELD:-}" = "1" ]; then
  exec "$@"
fi

# --- núcleos y número de slots --------------------------------------------------
if command -v nproc >/dev/null 2>&1; then
  cores="$(nproc)"
else
  cores="$(sysctl -n hw.ncpu)"
fi

if [[ "${HARNESS_BUILD_SLOTS:-}" =~ ^[0-9]+$ ]] && [ "${HARNESS_BUILD_SLOTS}" -ge 1 ]; then
  slots="$HARNESS_BUILD_SLOTS"      # override explícito
else
  slots=$(( cores / 4 ))            # VPS 6→1, Mac 14→3
  [ "$slots" -lt 1 ] && slots=1
fi

# --- dir de locks (por máquina y por uid; cross-sesión, cross-checkout) ---------
# En /tmp: se limpia solo en reboot y los flocks no sobreviven al proceso, así que
# no hay locks stale jamás.
dir="/tmp/harness-build-slots-$(id -u)"
mkdir -p "$dir"
chmod 700 "$dir"

# --- adquisición + exec, todo en UN proceso perl que sostiene el fd del lock ----
# El perl termina con exec del comando manteniendo abierto el fd del slot ganado; el
# kernel libera el lock al morir el proceso (aún con kill -9). $^F=255 evita que perl
# marque el fd como close-on-exec (si no, el exec liberaría el lock al instante).
export HARNESS_SLOT_DIR="$dir" HARNESS_SLOT_N="$slots"
# shellcheck disable=SC2016  # las comillas simples son a propósito: $dir/$N/@ARGV/$^F son de perl, NO de bash
exec /usr/bin/env perl -e '
use strict; use warnings;
use Fcntl qw(:flock SEEK_SET O_RDWR O_CREAT);
$^F = 255;                                  # fds del lock: NO close-on-exec (sobreviven al exec)

my $dir = $ENV{HARNESS_SLOT_DIR};
my $N   = $ENV{HARNESS_SLOT_N};
die "build-slot: entorno incompleto\n" unless defined $dir && defined $N && $N >= 1;

# (a) primera pasada: probar cada slot con LOCK_EX|LOCK_NB (no bloqueante).
for my $i (0 .. $N - 1) {
    open(my $fh, ">", "$dir/slot-$i.lock") or next;
    if (flock($fh, LOCK_EX | LOCK_NB)) {
        $ENV{HARNESS_BUILD_SLOT_HELD} = 1;
        exec { $ARGV[0] } @ARGV or die "build-slot: exec fallo: $!\n";
    }
    close($fh);                             # cerrar el fd del intento NB fallido antes de seguir
}

# (b) todos ocupados: tomar un ticket de un contador atomico (flock breve propio).
sysopen(my $tfh, "$dir/ticket", O_RDWR | O_CREAT, 0600)
    or die "build-slot: no pude abrir ticket: $!\n";
flock($tfh, LOCK_EX) or die "build-slot: no pude lockear ticket: $!\n";
seek($tfh, 0, SEEK_SET);
my $cur = <$tfh>;
$cur = (defined $cur && $cur =~ /^(\d+)/) ? $1 : 0;
my $ticket = $cur;
seek($tfh, 0, SEEK_SET);
truncate($tfh, 0);
print $tfh (($cur + 1) . "\n");
close($tfh);                                # libera el flock breve del ticket

# (c) bloquear (espera de kernel, cero CPU) sobre el slot que toca por turno.
my $slot = $ticket % $N;
# Emitimos la linea (bytes UTF-8 literales) ANTES de bloquear.
print STDERR "\xE2\x8F\xB3 build-slot: los $N slots estan ocupados \xE2\x80\x94 esperando (bloqueo de kernel, sin polling)\xE2\x80\xA6\n";
open(my $lfh, ">", "$dir/slot-$slot.lock") or die "build-slot: no pude abrir slot $slot: $!\n";
flock($lfh, LOCK_EX) or die "build-slot: no pude lockear slot $slot: $!\n";
$ENV{HARNESS_BUILD_SLOT_HELD} = 1;
exec { $ARGV[0] } @ARGV or die "build-slot: exec fallo: $!\n";
' -- "$@"
