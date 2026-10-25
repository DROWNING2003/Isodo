set -eu
bundle=$1
runtime=$2
mkdir -p "$runtime"
tar -xzf "$bundle" --strip-components=1 -C "$runtime"
# The bundle may gzip this native library inside the outer tar archive.
# fff-node's loader requires a real shared-library file, for either guest ISA.
for compressed in "$runtime"/node_modules/@ff-labs/fff-bin-*/libfff_c.so.gz; do
    [ -f "$compressed" ] || continue
    library=${compressed%.gz}
    gzip -dc "$compressed" > "$library.tmp"
    mv -f "$library.tmp" "$library"
done
