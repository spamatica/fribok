#!/bin/sh
# Installs the bundled fonts and help jars (not on Maven Central) into the
# local Maven repository. Run once from the project root before building.
set -e
cd "$(dirname "$0")"
for a in fonts help; do
  mvn -q install:install-file -Dfile=data/$a.jar -DgroupId=org.fribok \
    -DartifactId=$a -Dversion=0.1 -Dpackaging=jar
done
echo "Installed org.fribok:fonts:0.1 and org.fribok:help:0.1"
