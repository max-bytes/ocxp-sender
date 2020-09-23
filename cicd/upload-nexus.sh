#!/bin/sh

# TODO: integrate into upload image

if [ -z "$NEXUS_USER" ] || [ -z "$NEXUS_PASSWORD" ] || [ -z "$NEXUS_BASEURL" ] || [ -z "$NEXUS_REPONAME" ] || [ -z "$NEXUS_REPOPATH" ] || [ -z "$RPM_NAME" ] || [ -z "$CI_COMMIT_TAG" ] || [ -z "$CI_PIPELINE_ID" ]; then
  echo "some variables are missing bailing out..."
  echo ""
  echo "NEXUS_USER"
  echo "NEXUS_PASSWORD"
  echo "NEXUS_BASEURL"
  echo "NEXUS_REPONAME"
  echo "NEXUS_REPOPATH"
  echo "RPM_NAME"
  echo "CI_COMMIT_TAG"
  echo "CI_PIPELINE_ID"
  echo ""
  echo "... should be defined"
  exit 1
fi

sourcefile=$1
targetfile=$RPM_NAME-$CI_COMMIT_TAG-$CI_PIPELINE_ID

if [ -z "$sourcefile" ]; then
  echo "no sourcefile given..."
  exit 1
fi

if [ -z "$targetfile" ]; then
  echo "targetfile empty"
  exit 1
fi

echo "uploading $sourcefile to $NEXUS_BASEURL/repository/$NEXUS_REPONAME/$NEXUS_REPOPATH/$targetfile"

curl -v -u "$NEXUS_USER:$NEXUS_PASSWORD" --upload-file $1 $NEXUS_BASEURL/repository/$NEXUS_REPONAME/$NEXUS_REPOPATH/$targetfile
