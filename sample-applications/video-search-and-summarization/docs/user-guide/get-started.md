# Get Started

The Video Search and Summarization (VSS) sample application helps developers create a summary of long form video, search for the right video, and combine both search and summarization pipelines. This guide will help you set up, run, and modify the sample application on local and Edge AI systems.

This guide shows how to:

- **Set up the sample application**: Use Setup script to quickly deploy the application in your environment.
- **Run the application**: A single setup command brings up both Video Summarization and Video Search as two independent UI instances that share the same backend.
- **Modify application parameters**: Customize settings like inference models and deployment configurations to adapt the application to your specific requirements.

## Prerequisites

- Verify that your system meets the [minimum requirements](./get-started/system-requirements.md).
- Install Docker tool: [Installation Guide](https://docs.docker.com/get-docker/).
- Install Docker Compose tool: [Installation Guide](https://docs.docker.com/compose/install/).
- Install Python programming language v3.11

## Project Structure

The repository is organized as follows:

```text
sample-applications/video-search-and-summarization/
├── config                     # Configuration files
│   ├── nginx.conf             # NGINX configuration
│   └── rmq.conf               # RabbitMQ configuration
├── docker                     # Docker Compose files
│   ├── compose.base.yaml      # Base services configuration
│   ├── compose.summary.yaml   # Compose override file for video summarization services
│   ├── compose.vllm.yaml      # vLLM inference service overlay
│   ├── compose.search.yaml    # Compose override file for video search services
│   ├── compose.telemetry.yaml # Optional telemetry collector (vss-collector)
│   └── compose.gpu_ovms.yaml  # GPU configuration for OpenVINO™ model server
├── docs                       # Documentation
│   └── user-guide             # User guides and tutorials
├── pipeline-manager           # Backend service which orchestrates the video Summarization and search
├── search-ms                  # Video search microservice
├── ui                         # Video search and summarization UI code
├── build.sh                   # Script for building application images
├── setup.sh                   # Setup script for environment and deployment
└── README.md                  # Project documentation
```

## Set Required Environment Variables

Before running the application, you need to set several environment variables:

1. **Configure the registry**:
   The application uses registry URL and tag to pull the required images.

   ```bash
   export REGISTRY_URL=intel
   export TAG=latest
   ```

2. **Set required credentials for some services**:
   Following variables **MUST** be set on your current shell before running the setup script:

   ```bash
   # MinIO credentials (object storage)
   export MINIO_ROOT_USER=<your-minio-username>
   export MINIO_ROOT_PASSWORD=<your-minio-password>

   # PostgreSQL credentials (database)
   export POSTGRES_USER=<your-postgres-username>
   export POSTGRES_PASSWORD=<your-postgres-password>

   # RabbitMQ credentials (message broker)
   export RABBITMQ_USER=<your-rabbitmq-username>
   export RABBITMQ_PASSWORD=<your-rabbitmq-password>
   ```

3. **Set environment variables for customizing model selection**:

   You **must** set these environment variables on your current shell. Setting these variables help you customize the models used for deployment.

   ```bash
   # For VLM-based chunk captioning and video summarization on CPU
   export VLM_MODEL_NAME="Qwen/Qwen2.5-VL-3B-Instruct"  # or any other supported VLM model on CPU

   # For VLM-based chunk captioning and video summarization on GPU
   export VLM_MODEL_NAME="microsoft/Phi-3.5-vision-instruct"  # or any other supported VLM model on GPU

   # (Optional) For OVMS-based video summarization (when using with ENABLE_OVMS_LLM_SUMMARY=true or ENABLE_OVMS_LLM_SUMMARY_GPU=true)
   export OVMS_LLM_MODEL_NAME="Intel/neural-chat-7b-v3-3"  # or any other supported LLM model

   # Model used by Audio Analyzer service. Only Whisper models variants are supported.
   # Common Supported models: tiny.en, small.en, medium.en, base.en, large-v1, large-v2, large-v3.
   # You can provide just one or comma-separated list of models.
   export ENABLED_WHISPER_MODELS="tiny.en,small.en,medium.en"

   # Object detection model used for Video Ingestion Service. Only Yolo models are supported.
   export OD_MODEL_NAME="yolov8l-worldv2"

   # Multimodal embedding model used by the default (--up) deployment and by --all.
   export EMBEDDING_MODEL_NAME="CLIP/clip-vit-b-32"

   # Text embedding model. Required ONLY for --all mode. Ignored by the default --up mode.
   export TEXT_EMBEDDING_MODEL_NAME="QwenText/qwen3-embedding-0.6b"
   ```

   > **Note**:
   >
   > - In the **default (`--up`) mode**, only `EMBEDDING_MODEL_NAME` is required. It is used by the multimodal embedding service and produces embeddings that are stored in the `video_frame_embeddings` vector-DB index.
   > - In **`--all` mode**, both `EMBEDDING_MODEL_NAME` and `TEXT_EMBEDDING_MODEL_NAME` are required. The setup script validates both variables and uses `TEXT_EMBEDDING_MODEL_NAME` to override `EMBEDDING_MODEL_NAME` so that embeddings are generated over the text of summaries and stored in the `video_summary_embeddings` vector-DB index.
   > - Review the supported model list in [supported-models](https://github.com/open-edge-platform/edge-ai-libraries/blob/main/microservices/multimodal-embedding-serving/docs/user-guide/supported-models.md) before choosing model IDs.

4. **Configure Directory Watcher (Video Search)**:

   For automated video ingestion into the search pipeline, you can use the directory watcher service:

   ```bash
   # Path to the directory to watch on the host system. Default: "edge-ai-libraries/sample-applications/video-search-and-summarization/data"
   export VS_WATCHER_DIR="/path/to/your/video/directory"
   ```

   > **📁 Directory Watcher**: For complete setup instructions, configuration options, and usage details, see the [Directory Watcher Service Guide](./directory-watcher-guide.md). The watcher feeds the Video Search pipeline, which is always part of the unified deployment.

5. **Control the frame extraction interval (Video Search)**:

   The DataPrep microservice samples frames from uploaded videos according to the `FRAME_INTERVAL` environment variable. Set this variable before running `source setup.sh` to control how often frames are selected for processing.

   ```bash
   export FRAME_INTERVAL=15
   ```

   In the example above, DataPrep processes every fifteenth frame: each selected frame (optionally after object detection) is converted into embeddings and stored in the vector database. Lower values improve recall at the cost of higher compute and storage usage, while higher values reduce processing load but may skip important frames. If you do not set this variable, the service falls back to its configured default.

6. **Enable ROI consolidation (Video Search)**:

   ROI consolidation groups overlapping object detections into merged regions of interest (ROIs) before cropping for embeddings. Enable this feature and tune it with the following environment variables:

   ```bash
   # Enable ROI consolidation (default: false)
   export ROI_CONSOLIDATION_ENABLED=true

   # IoU threshold for grouping ROIs (higher = stricter merging)
   export ROI_CONSOLIDATION_IOU_THRESHOLD=0.2

   # Only merge ROIs with the same class label when true
   export ROI_CONSOLIDATION_CLASS_AWARE=false

   # Expand merged ROIs by a fraction of width/height
   export ROI_CONSOLIDATION_CONTEXT_SCALE=0.2
   ```

   The IoU calculation follows the standard formula:

   $$
   IoU(A, B) = \frac{|A \cap B|}{|A \cup B|}
   $$

   > **Note:** Enabling ROI consolidation can improve search relevance by creating more meaningful regions for embedding, but it may also increase processing time.

7. **Set advanced VLM Configuration Options**:

   The following environment variables provide additional control over VLM inference behavior and logging:

   ```bash
   # (Optional) OpenVINO configuration for VLM inference optimization
   # Pass OpenVINO configuration parameters as a JSON string to fine-tune inference performance
   # Default latency-optimized configuration (equivalent to not setting OV_CONFIG)
   # export OV_CONFIG='{"PERFORMANCE_HINT": "LATENCY"}'

   # Throughput-optimized configuration
   export OV_CONFIG='{"PERFORMANCE_HINT": "THROUGHPUT"}'
   ```

   > **IMPORTANT:** The `OV_CONFIG` variable is used to pass OpenVINO configuration parameters to the VLM service. It allows you to optimize inference performance based on your hardware and workload.
   > For a complete list of OpenVINO configuration options, refer to the [OpenVINO Documentation](https://docs.openvino.ai/2025/openvino-workflow/running-inference/inference-devices-and-modes.html).
   > **Note**: If OV_CONFIG is not set, the default configuration `{"PERFORMANCE_HINT": "LATENCY"}` will be used.

8. **(Optional) Telemetry collection**:

   The deployment can start a lightweight telemetry collector (`vss-collector`) that streams CPU/RAM/GPU metrics to the Pipeline Manager and renders them in the UI.

   ```bash
   # Disabled by default
   export ENABLE_VSS_COLLECTOR=false

   # Enable the collector if you want telemetry
   export ENABLE_VSS_COLLECTOR=true
   ```

9. **(Optional) Customize UI host ports**:

   Two UI instances — Video Summarization and Video Search — are always brought up together, each fronted by its own nginx proxy. Override the host ports if the defaults are already in use on your system.

   ```bash
   # Nginx proxy port for the Video Summarization UI (default: 12345)
   export APP_SUMMARY_HOST_PORT=12345

   # Nginx proxy port for the Video Search UI (default: 12346)
   export APP_SEARCH_HOST_PORT=12346

   # Direct-access UI container ports (default: 9998 and 9999)
   export UI_SUMMARY_HOST_PORT=9998
   export UI_SEARCH_HOST_PORT=9999
   ```

   > **Note:** `APP_SUMMARY_HOST_PORT` and `APP_SEARCH_HOST_PORT` must be different; the setup script errors out if they collide.

**🔐 Work with Gated Models**

To run a **GATED MODEL** like Llama models, you will need to pass your [huggingface token](https://huggingface.co/docs/hub/security-tokens#user-access-tokens). You will need to request for an access to a specific model by going to the respective model page on Hugging Face website.

Go to <https://huggingface.co/settings/tokens> to get your token.

```bash
export GATED_MODEL=true
export HUGGINGFACE_TOKEN=<your_huggingface_token>
```

Once exported, run the setup script as mentioned [here](#run-the-application). Switch off the `GATED_MODEL` flag by running `export GATED_MODEL=false`, once you no longer use gated models. This avoids unnecessary authentication step during setup.

## Application Overview

The Video Search and Summarization application always brings up **two UI instances in parallel** — one scoped to Video Summarization, one scoped to Video Search — fronted by separate nginx proxies and backed by a shared pipeline-manager. Both capabilities are always available to the user.

| UI Instance | Capability | Default URL |
|-------------|-------------|------|
| Video Summarization UI | Video frame captioning and summarization | `http://<host-ip>:12345` |
| Video Search UI | Video indexing and semantic search | `http://<host-ip>:12346` |

Both UIs are built from the same image and only differ in the feature flags set on the container, so runtime behavior and look-and-feel are consistent.

Two deployment modes are supported. They differ only in which embedding model is used for Video Search and which vector-DB index the Search UI queries; the UI surface is identical in both.

| Mode | Setup command | Required embedding variables | Vector-DB index | When to use |
|------|---------------|------------------------------|-----------------|-------------|
| Default | `source setup.sh` or `source setup.sh --up` | `EMBEDDING_MODEL_NAME` | `video_frame_embeddings` | Search over raw video frame embeddings produced by a multimodal model. |
| All | `source setup.sh --all` | `EMBEDDING_MODEL_NAME` **and** `TEXT_EMBEDDING_MODEL_NAME` | `video_summary_embeddings` | Search over the text of generated summaries using a dedicated text embedding model. |

> **Automated Video Ingestion**: The Video Search pipeline includes an optional Directory Watcher service for automated video processing. See the [Directory Watcher Service Guide](./directory-watcher-guide.md) for details.

### Deployment Options for Video Summarization

| Deployment Option | Chunk-Wise Summary<sup>(1)</sup> Configuration | Final Summary<sup>(2)</sup> Configuration | Environment Variables to Set | Recommended Models | Recommended Usage Model |
|--------|--------------------|---------------------|-----------------------|----------------|----------------|
| VLM-CPU | vlm-openvino-serving on CPU | vlm-openvino-serving on CPU | Default | VLM: `Qwen/Qwen2.5-VL-3B-Instruct` | For usage with CPUs only; when inference speed is not a priority. |
| VLM-GPU | vlm-openvino-serving | vlm-openvino-serving GPU | `ENABLE_VLM_GPU=true` | VLM: `microsoft/Phi-3.5-vision-instruct` | For usage with CPUs and GPUs; when inference speed is a priority. |
| VLM-CPU-OVMS-CPU | vlm-openvino-serving on CPU | OVMS Microservice on CPU | `ENABLE_OVMS_LLM_SUMMARY=true` | VLM: `Qwen/Qwen2.5-VL-3B-Instruct`<br>LLM: `Intel/neural-chat-7b-v3-3` | For usage with CPUs and microservices; when inference speed is not a priority. |
| VLM-CPU-OVMS-GPU | vlm-openvino-serving on CPU | OVMS Microservice on GPU | `ENABLE_OVMS_LLM_SUMMARY_GPU=true` | VLM: `Qwen/Qwen2.5-VL-3B-Instruct`<br>LLM: `Intel/neural-chat-7b-v3-3` | For usage with CPUs, GPUs, and microservices; when inference speed is a priority. |
| VLM-GPU-OVMS-CPU | vlm-openvino-serving on GPU | OVMS Microservice on CPU | `ENABLE_VLM_GPU=true` `ENABLE_OVMS_LLM_SUMMARY=true` | VLM: `Qwen/Qwen2.5-VL-3B-Instruct`<br>LLM: `Intel/neural-chat-7b-v3-3` | For usage with CPUs, GPUs, and microservices; when inference speed is a priority. |
| vLLM-CPU | vLLM serving on CPU | vLLM Service on CPU | `ENABLE_VLLM=true` | VLM: `Qwen/Qwen2.5-VL-3B-Instruct` | Deploy on Intel® Xeon® Processors without GPU requirements. |
> **Note:**
>
> 1) Chunk-Wise Summary is a method of summarization where it breaks videos into chunks and then summarizes each chunk.
> 2) Final Summary is a method of summarization where it summarizes the whole video.
> 3) If both VLM and LLM is configured for GPU, VLM will be prioritized for GPU and LLM reset to CPU.

## Using Edge Microvisor Toolkit

If you are running the VSS application on an OS image built with **Edge Microvisor Toolkit** — an Azure Linux-based build pipeline for Intel® platforms — follow the below listed guidelines. The guidelines vary based on the flavor of Edge Microvisor Toolkit used and the user is encouraged to refer to detailed documentation for [EMT-D](https://github.com/open-edge-platform/edge-microvisor-toolkit/blob/3.0/docs/developer-guide/emt-architecture-overview.md#developer-node-mutable-iso-image) and [EMT-S](https://github.com/open-edge-platform/edge-microvisor-toolkit-standalone-node). A few specific dependencies are called out below.

Install the `mesa-libGL` package. Installing `mesa-libGL` provides the OpenGL library which is needed by the `Audio Analyzer service`. Depending on `EMT-D` or `EMT-S`, the steps vary.

For `EMT-D`, the following steps should work.

```bash
sudo dnf install mesa-libGL
# If you are using TDNF, you can use the following command to install:
sudo tdnf search mesa-libGL
sudo tdnf install mesa-libGL
```

For `EMT-S`,

```bash
sudo env no_proxy="localhost,127.0.0.1" dnf --installroot=/opt/user-apps/tools/ -y install mesa-libGL
export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/opt/user-apps/tools/usr/lib/
```

Additional tools and packages that should be installed includes `git` and `wget`. The instructions for the same is available in the detailed `EMT-S` and `EMT-D` documentations. The instructions work for any other required packages too.

## Run the Application

Follow these steps to run the application:

1. Clone the repository and navigate to the project directory:

   ```bash
   # Clone the latest on mainline
   git clone https://github.com/open-edge-platform/edge-ai-libraries.git edge-ai-libraries
   # Alternatively, clone a specific release branch
   git clone https://github.com/open-edge-platform/edge-ai-libraries.git edge-ai-libraries -b <release-tag>

   cd edge-ai-libraries/sample-applications/video-search-and-summarization
   ```

2. [Set the required environment variables](#set-required-environment-variables).

3. Run the setup script. A single invocation brings up both the Video Summarization and Video Search UIs.

   > **Note:** To bring down a running deployment before starting again, run:

   ```bash
   source setup.sh --down
   ```

   > **💡 Clean-up Tip**: If you encounter issues or want to completely reset the application data, use `source setup.sh --clean-data` to stop all containers and remove all Docker volumes including user data. This provides a fresh start for troubleshooting.

   - **Default mode — bring up the full application (both Summary and Search UIs):**

     ```bash
     source setup.sh --up
     ```

     Running `source setup.sh` with no argument is equivalent to `source setup.sh --up`. The Search UI indexes and queries the `video_frame_embeddings` vector-DB collection using the multimodal model configured via `EMBEDDING_MODEL_NAME`.

     When the script finishes, it prints the URLs for the two UIs, for example:

     ```text
     Two UI instances are now available:
       • Video Summarization UI: http://<host-ip>:12345
       • Video Search UI:        http://<host-ip>:12346
     ```

   - **`--all` mode — unified Summary + Search with text-embedding search over summaries:**

     ```bash
     source setup.sh --all
     ```

     `--all` brings up the same two UIs as `--up`, but the Search UI indexes and queries the `video_summary_embeddings` collection using the dedicated text-embedding model configured via `TEXT_EMBEDDING_MODEL_NAME`. Both `EMBEDDING_MODEL_NAME` and `TEXT_EMBEDDING_MODEL_NAME` must be set before invoking this mode.

   > **Telemetry**: The telemetry collector is disabled by default in both modes. Enable it with:
   >
   > ```bash
   > ENABLE_VSS_COLLECTOR=true source setup.sh --up
   > # or
   > ENABLE_VSS_COLLECTOR=true source setup.sh --all
   > ```

   > **📁 Directory Watcher**: For automated video ingestion into the Search pipeline, see the [Directory Watcher Service Guide](./directory-watcher-guide.md).

   > **Legacy flags**: `--summary` and `--search` are still accepted for backward compatibility. They behave like `--up` and print a deprecation notice; they no longer launch a single-mode deployment. `--all` is **not** deprecated and remains a first-class mode.

   - **Use OpenVINO model server microservice for the final summary:**

    ```bash
    ENABLE_OVMS_LLM_SUMMARY=true source setup.sh --up
    ```

- **Use vLLM as the only inference backend:**

    ```bash
    ENABLE_VLLM=true source setup.sh --up
    ```

    > **Note:**
    > - The vLLM configuration has been tested on Intel® Xeon® 6 processors.
    > - Review [docker/compose.vllm.yaml](https://github.com/open-edge-platform/edge-ai-libraries/blob/main/sample-applications/video-search-and-summarization/docker/compose.vllm.yaml) to understand the VLLM engine and environment variables exposed. Modify it as per your use case. Refer to the [vLLM Engine Arguments documentation](https://docs.vllm.ai/en/stable/configuration/engine_args/) and [vLLM Environment Variables documentation](https://docs.vllm.ai/en/stable/configuration/env_vars/) for more details.

4. (Optional) Verify the resolved environment variables and setup configurations:

   ```bash
   # To just set environment variables without starting containers
   source setup.sh --setenv

   # To see the fully resolved compose configuration for the default deployment
   source setup.sh --up config

   # To see the fully resolved compose configuration for the --all deployment
   source setup.sh --all config

   # To see the resolved configuration with OpenVINO model server on CPU
   ENABLE_OVMS_LLM_SUMMARY=true source setup.sh --up config

   # To see the resolved configuration with vLLM enabled
   ENABLE_VLLM=true source setup.sh --up config
   ```

### Use GPU Acceleration

To use GPU acceleration for VLM inference:

> **Note:** To bring down a running deployment before re-running with different flags, run:
>
> ```bash
> source setup.sh --down
> ```

```bash
ENABLE_VLM_GPU=true source setup.sh --up
```

To use GPU acceleration for OpenVINO model server-based summarization:

```bash
ENABLE_OVMS_LLM_SUMMARY_GPU=true source setup.sh --up
```

To use GPU acceleration for the multimodal embedding service used by search:

```bash
ENABLE_EMBEDDING_GPU=true source setup.sh --up
```

To verify the configuration and resolved environment variables without running the application:

```bash
# For VLM inference on GPU
ENABLE_VLM_GPU=true source setup.sh --up config
```

```bash
# For OVMS inference on GPU
ENABLE_OVMS_LLM_SUMMARY_GPU=true source setup.sh --up config
```

```bash
# For embedding service on GPU
ENABLE_EMBEDDING_GPU=true source setup.sh --up config
```

> **Note:** Avoid setting the `ENABLE_VLM_GPU`, `ENABLE_OVMS_LLM_SUMMARY_GPU`, or `ENABLE_EMBEDDING_GPU` flags explicitly on the shell using `export`, because you need to switch these flags off as well, to return to the CPU configuration.

## Access the Application

After successfully starting the application, two independent UIs are exposed by two nginx proxies:

| UI | URL | Purpose |
|----|-----|---------|
| Video Summarization | `http://<host-ip>:12345` | Upload a video and generate chunk-wise and final summaries. |
| Video Search       | `http://<host-ip>:12346` | Index videos and run semantic search over them. |

Both UIs share the same backend services (pipeline-manager, MinIO, databases, embedding service, etc.), so videos and embeddings ingested from one UI are visible to the other.

The host ports are customizable via the `APP_SUMMARY_HOST_PORT` (default `12345`) and `APP_SEARCH_HOST_PORT` (default `12346`) environment variables; they must be different values.

## CLI Usage

Refer to [CLI Usage](https://github.com/open-edge-platform/edge-ai-libraries/blob/main/sample-applications/video-search-and-summarization/cli/README.md) for details on using the application from a text user interface (terminal-based UI).

## Running in Kubernetes Cluster

Refer to [Deploy with Helm](./deploy-with-helm.md) for the details. Ensure the prerequisites mentioned on this page are addressed before proceeding to deploy with Helm chart.

## Advanced Setup Options

For alternative ways to set up the sample application, see [How to Build from Source](./build-from-source.md)

## Supporting Resources

- [How it works](./how-it-works.md)
- [Troubleshooting](./troubleshooting.md)
- [Docker Compose Documentation](https://docs.docker.com/compose/)

<!--hide_directive
:::{toctree}
:hidden:

get-started/system-requirements

:::
hide_directive-->
