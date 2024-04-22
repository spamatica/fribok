#!/bin/bash
mvn install:install-file -Dfile=data/help.jar -DgroupId=org.fribok -DartifactId=help -Dversion=0.1 -DgeneratePom=true -Dpackaging=jar
mvn install:install-file -Dfile=data/fonts.jar -DgroupId=org.fribok -DartifactId=fonts -Dversion=0.1 -DgeneratePom=true -Dpackaging=jar
